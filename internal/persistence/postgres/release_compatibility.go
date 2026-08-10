package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

const OVNRuntimeWorkSchemaV1 = "kim.network.ovn-runtime-work/v1"

var ErrIncompatibleRelease = errors.New("component release is incompatible with current release authority")
var ErrReleaseFeatureGateBlocked = errors.New("release feature gate is blocked by current component bindings")

type ReleaseManifest struct {
	ReleaseID, ProductVersion, Channel, ManifestDigest        string
	ManifestRevision                                          uint64
	CertificationState                                        string
	OVNWorkerArtifactDigest, OVNWorkerComponentContractDigest string
	OVNWorkerWorkSchemas                                      []string
}

type ReleaseCompatibilityEdge struct {
	SourceReleaseID, TargetReleaseID               string
	SourceManifestRevision, TargetManifestRevision uint64
	AllowedWorkSchemas                             []string
	EdgeDigest, CertificationState                 string
}

type OVNWorkerReleaseRequest struct {
	WorkerID, ReleaseID, ArtifactDigest, EvaluatorArtifactDigest string
	ManifestRevision                                             uint64
	SupportedWorkSchemas                                         []string
}

type ComponentReleaseBinding struct {
	ComponentID, ReleaseID, ArtifactDigest, CompatibilityState, LifecycleState, DecisionID string
	ManifestRevision, BindingGeneration                                                    uint64
	SupportedWorkSchemas                                                                   []string
}

type ReleaseDistrustRequest struct {
	DistrustID, Scope, SourceReleaseID, TargetReleaseID, Reason, EvaluatorArtifactDigest, EvidenceDigest string
	SourceManifestRevision, TargetManifestRevision                                                       uint64
}

func PublishReleaseManifest(ctx context.Context, db TxBeginner, manifest ReleaseManifest) error {
	manifest.OVNWorkerWorkSchemas = canonicalSchemas(manifest.OVNWorkerWorkSchemas)
	if manifest.ReleaseID == "" || manifest.ProductVersion == "" || manifest.ManifestRevision == 0 ||
		!validDigest(manifest.ManifestDigest) || !validDigest(manifest.OVNWorkerArtifactDigest) ||
		!validDigest(manifest.OVNWorkerComponentContractDigest) || len(manifest.OVNWorkerWorkSchemas) == 0 {
		return errors.New("complete immutable release manifest is required")
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.release_manifest_evidence(
			release_id,manifest_revision,product_version,channel,manifest_digest,certification_state
		) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, manifest.ReleaseID, manifest.ManifestRevision,
			manifest.ProductVersion, manifest.Channel, manifest.ManifestDigest, manifest.CertificationState); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.release_manifest_component_evidence(
			release_id,manifest_revision,component_type,artifact_digest,supported_work_schema_versions,component_contract_digest
		) VALUES($1,$2,'OVN_RUNTIME_WORKER',$3,$4,$5) ON CONFLICT DO NOTHING`, manifest.ReleaseID,
			manifest.ManifestRevision, manifest.OVNWorkerArtifactDigest, manifest.OVNWorkerWorkSchemas, manifest.OVNWorkerComponentContractDigest); err != nil {
			return err
		}
		var identical bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM kim.release_manifest_evidence manifest
			JOIN kim.release_manifest_component_evidence component USING(release_id,manifest_revision)
			WHERE manifest.release_id=$1 AND manifest.manifest_revision=$2 AND manifest.product_version=$3
			 AND manifest.channel=$4 AND manifest.manifest_digest=$5 AND manifest.certification_state=$6
			 AND component.component_type='OVN_RUNTIME_WORKER' AND component.artifact_digest=$7
			 AND component.supported_work_schema_versions=$8 AND component.component_contract_digest=$9
		)`, manifest.ReleaseID, manifest.ManifestRevision, manifest.ProductVersion, manifest.Channel,
			manifest.ManifestDigest, manifest.CertificationState, manifest.OVNWorkerArtifactDigest,
			manifest.OVNWorkerWorkSchemas, manifest.OVNWorkerComponentContractDigest).Scan(&identical); err != nil {
			return err
		}
		if !identical {
			return errors.New("release manifest digest conflict")
		}
		return nil
	})
}

func PublishReleaseCompatibilityEdge(ctx context.Context, db TxBeginner, edge ReleaseCompatibilityEdge) error {
	edge.AllowedWorkSchemas = canonicalSchemas(edge.AllowedWorkSchemas)
	if edge.SourceReleaseID == "" || edge.TargetReleaseID == "" || edge.SourceManifestRevision == 0 || edge.TargetManifestRevision == 0 || len(edge.AllowedWorkSchemas) == 0 || !validDigest(edge.EdgeDigest) {
		return errors.New("complete explicit release compatibility edge is required")
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		command, err := tx.Exec(ctx, `INSERT INTO kim.release_compatibility_edge_evidence(
		source_release_id,source_manifest_revision,target_release_id,target_manifest_revision,
		edge_type,allowed_work_schema_versions,edge_digest,certification_state
	) VALUES($1,$2,$3,$4,'N_MINUS_ONE',$5,$6,$7) ON CONFLICT DO NOTHING`, edge.SourceReleaseID,
			edge.SourceManifestRevision, edge.TargetReleaseID, edge.TargetManifestRevision, edge.AllowedWorkSchemas,
			edge.EdgeDigest, edge.CertificationState)
		if err != nil {
			return err
		}
		if command.RowsAffected() == 1 {
			return nil
		}
		var identical bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.release_compatibility_edge_evidence
		WHERE source_release_id=$1 AND source_manifest_revision=$2 AND target_release_id=$3
		 AND target_manifest_revision=$4 AND allowed_work_schema_versions=$5 AND edge_digest=$6
		 AND certification_state=$7)`, edge.SourceReleaseID, edge.SourceManifestRevision, edge.TargetReleaseID,
			edge.TargetManifestRevision, edge.AllowedWorkSchemas, edge.EdgeDigest, edge.CertificationState).Scan(&identical); err != nil {
			return err
		}
		if !identical {
			return errors.New("release compatibility edge digest conflict")
		}
		return nil
	})
}

func SetReleaseAuthority(ctx context.Context, db TxBeginner, releaseID string, revision uint64, workSchema, lifecycle string) error {
	if releaseID == "" || revision == 0 || workSchema == "" {
		return errors.New("complete release authority is required")
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var targetDistrusted bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.release_distrust_evidence
			WHERE distrust_scope='MANIFEST' AND source_release_id=$1 AND source_manifest_revision=$2)`,
			releaseID, revision).Scan(&targetDistrusted); err != nil || targetDistrusted {
			return ErrIncompatibleRelease
		}
		var currentID *string
		var currentRevision *int64
		if err := tx.QueryRow(ctx, `SELECT release_id,manifest_revision FROM kim.release_authority_current WHERE singleton=true FOR UPDATE`).Scan(&currentID, &currentRevision); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if currentID != nil && (*currentID != releaseID || uint64(*currentRevision) != revision) {
			var allowed bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.release_compatibility_edge_evidence edge
				WHERE source_release_id=$1 AND source_manifest_revision=$2 AND target_release_id=$3
				 AND target_manifest_revision=$4 AND certification_state='VALIDATED'
				 AND NOT EXISTS(SELECT 1 FROM kim.release_distrust_evidence distrust
					WHERE distrust.distrust_scope='COMPATIBILITY_EDGE'
					 AND distrust.source_release_id=edge.source_release_id
					 AND distrust.source_manifest_revision=edge.source_manifest_revision
					 AND distrust.target_release_id=edge.target_release_id
					 AND distrust.target_manifest_revision=edge.target_manifest_revision))`, *currentID, *currentRevision, releaseID, revision).Scan(&allowed); err != nil || !allowed {
				return ErrIncompatibleRelease
			}
		}
		var supported bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.release_manifest_component_evidence
			WHERE release_id=$1 AND manifest_revision=$2 AND component_type='OVN_RUNTIME_WORKER'
			 AND $3=ANY(supported_work_schema_versions))`, releaseID, revision, workSchema).Scan(&supported); err != nil || !supported {
			return ErrIncompatibleRelease
		}
		_, err := tx.Exec(ctx, `INSERT INTO kim.release_authority_current(
			release_id,manifest_revision,authority_generation,write_work_schema_version,lifecycle_state
		) VALUES($1,$2,1,$3,$4) ON CONFLICT(singleton) DO UPDATE SET
			release_id=EXCLUDED.release_id,manifest_revision=EXCLUDED.manifest_revision,
			authority_generation=kim.release_authority_current.authority_generation+1,
			write_work_schema_version=EXCLUDED.write_work_schema_version,lifecycle_state=EXCLUDED.lifecycle_state,
			updated_at=statement_timestamp()
			WHERE kim.release_authority_current.release_id<>EXCLUDED.release_id
			 OR kim.release_authority_current.manifest_revision<>EXCLUDED.manifest_revision
			 OR kim.release_authority_current.write_work_schema_version<>EXCLUDED.write_work_schema_version
			 OR kim.release_authority_current.lifecycle_state<>EXCLUDED.lifecycle_state`, releaseID, revision, workSchema, lifecycle)
		return err
	})
}

func RegisterOVNWorkerRelease(ctx context.Context, db TxBeginner, request OVNWorkerReleaseRequest) (ComponentReleaseBinding, error) {
	request.SupportedWorkSchemas = canonicalSchemas(request.SupportedWorkSchemas)
	if request.WorkerID == "" || request.ReleaseID == "" || request.ManifestRevision == 0 || !validDigest(request.ArtifactDigest) || !validDigest(request.EvaluatorArtifactDigest) || len(request.SupportedWorkSchemas) == 0 {
		return ComponentReleaseBinding{}, errors.New("complete OVN worker release evidence is required")
	}
	var binding ComponentReleaseBinding
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var targetID, writeSchema string
		var targetRevision, authorityGeneration int64
		if err := tx.QueryRow(ctx, `SELECT release_id,manifest_revision,authority_generation,write_work_schema_version
			FROM kim.release_authority_current WHERE singleton=true AND lifecycle_state IN ('ACTIVE','ROLLING') FOR SHARE`).Scan(&targetID, &targetRevision, &authorityGeneration, &writeSchema); err != nil {
			return fmt.Errorf("current release authority: %w", err)
		}
		var manifestArtifact string
		var manifestSchemas []string
		var certification string
		if err := tx.QueryRow(ctx, `SELECT component.artifact_digest,component.supported_work_schema_versions,manifest.certification_state
			FROM kim.release_manifest_evidence manifest JOIN kim.release_manifest_component_evidence component USING(release_id,manifest_revision)
			WHERE manifest.release_id=$1 AND manifest.manifest_revision=$2 AND component.component_type='OVN_RUNTIME_WORKER'`,
			request.ReleaseID, request.ManifestRevision).Scan(&manifestArtifact, &manifestSchemas, &certification); err != nil {
			return fmt.Errorf("observed release manifest: %w", err)
		}
		decision, reason := "INCOMPATIBLE", "artifact_or_schema_manifest_mismatch"
		manifestSchemas = canonicalSchemas(manifestSchemas)
		var manifestDistrusted bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.release_distrust_evidence
			WHERE distrust_scope='MANIFEST' AND source_release_id=$1 AND source_manifest_revision=$2)`,
			request.ReleaseID, request.ManifestRevision).Scan(&manifestDistrusted); err != nil {
			return err
		}
		if manifestDistrusted {
			reason = "release_manifest_distrusted"
		} else if manifestArtifact == request.ArtifactDigest && slices.Equal(manifestSchemas, request.SupportedWorkSchemas) && certification == "VALIDATED" {
			if request.ReleaseID == targetID && request.ManifestRevision == uint64(targetRevision) && slices.Contains(request.SupportedWorkSchemas, writeSchema) {
				decision, reason = "VALIDATED", "current_release_manifest_match"
			} else {
				var edgeAllowed bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.release_compatibility_edge_evidence
					edge WHERE source_release_id=$1 AND source_manifest_revision=$2 AND target_release_id=$3
					 AND target_manifest_revision=$4 AND certification_state='VALIDATED'
					 AND $5=ANY(allowed_work_schema_versions)
					 AND NOT EXISTS(SELECT 1 FROM kim.release_distrust_evidence distrust
						WHERE distrust.distrust_scope='COMPATIBILITY_EDGE'
						 AND distrust.source_release_id=edge.source_release_id
						 AND distrust.source_manifest_revision=edge.source_manifest_revision
						 AND distrust.target_release_id=edge.target_release_id
						 AND distrust.target_manifest_revision=edge.target_manifest_revision))`, request.ReleaseID, request.ManifestRevision,
					targetID, targetRevision, writeSchema).Scan(&edgeAllowed); err != nil {
					return err
				}
				if edgeAllowed && slices.Contains(request.SupportedWorkSchemas, writeSchema) {
					decision, reason = "COMPATIBLE", "explicit_n_minus_one_edge"
				} else {
					reason = "missing_explicit_edge_or_write_schema"
				}
			}
		}
		decisionInput := map[string]any{"subject": request.WorkerID, "release": request.ReleaseID, "revision": request.ManifestRevision,
			"artifact": request.ArtifactDigest, "schemas": request.SupportedWorkSchemas, "target": targetID,
			"target_revision": targetRevision, "authority_generation": authorityGeneration, "decision": decision, "reason": reason,
			"evaluator": request.EvaluatorArtifactDigest}
		raw, _ := json.Marshal(decisionInput)
		evidenceDigest := digestReleaseBytes(raw)
		decisionID := "compat:" + evidenceDigest
		if _, err := tx.Exec(ctx, `INSERT INTO kim.compatibility_decision_evidence(
			decision_id,subject_id,component_type,observed_release_id,observed_manifest_revision,
			observed_artifact_digest,supported_work_schema_versions,target_release_id,target_manifest_revision,
			release_authority_generation,decision,reason,evaluator_artifact_digest,evidence_digest
		) VALUES($1,$2,'OVN_RUNTIME_WORKER',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT DO NOTHING`,
			decisionID, request.WorkerID, request.ReleaseID, request.ManifestRevision, request.ArtifactDigest,
			request.SupportedWorkSchemas, targetID, targetRevision, authorityGeneration, decision, reason,
			request.EvaluatorArtifactDigest, evidenceDigest); err != nil {
			return err
		}
		lifecycle := "FENCED"
		if decision == "VALIDATED" || decision == "COMPATIBLE" {
			lifecycle = "ACTIVE"
		}
		var existingGeneration int64
		var same bool
		err := tx.QueryRow(ctx, `SELECT binding_generation,
			(release_id=$2 AND manifest_revision=$3 AND artifact_digest=$4 AND supported_work_schema_versions=$5
			 AND compatibility_decision_id=$6 AND compatibility_state=$7 AND lifecycle_state=$8)
			FROM kim.component_release_bindings_current WHERE component_id=$1 FOR UPDATE`, request.WorkerID,
			request.ReleaseID, request.ManifestRevision, request.ArtifactDigest, request.SupportedWorkSchemas,
			decisionID, decision, lifecycle).Scan(&existingGeneration, &same)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		generation := int64(1)
		if err == nil {
			generation = existingGeneration
			if !same {
				generation++
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.component_release_bindings_current(
			component_id,component_type,release_id,manifest_revision,artifact_digest,supported_work_schema_versions,
			binding_generation,compatibility_decision_id,compatibility_state,lifecycle_state
		) VALUES($1,'OVN_RUNTIME_WORKER',$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT(component_id) DO UPDATE SET release_id=EXCLUDED.release_id,manifest_revision=EXCLUDED.manifest_revision,
		 artifact_digest=EXCLUDED.artifact_digest,supported_work_schema_versions=EXCLUDED.supported_work_schema_versions,
		 binding_generation=EXCLUDED.binding_generation,compatibility_decision_id=EXCLUDED.compatibility_decision_id,
		 compatibility_state=EXCLUDED.compatibility_state,lifecycle_state=EXCLUDED.lifecycle_state,updated_at=statement_timestamp()`,
			request.WorkerID, request.ReleaseID, request.ManifestRevision, request.ArtifactDigest,
			request.SupportedWorkSchemas, generation, decisionID, decision, lifecycle); err != nil {
			return err
		}
		binding = ComponentReleaseBinding{ComponentID: request.WorkerID, ReleaseID: request.ReleaseID,
			ManifestRevision: request.ManifestRevision, ArtifactDigest: request.ArtifactDigest,
			SupportedWorkSchemas: request.SupportedWorkSchemas, BindingGeneration: uint64(generation),
			CompatibilityState: decision, LifecycleState: lifecycle, DecisionID: decisionID}
		return nil
	})
	return binding, err
}

func RevokeReleaseTrust(ctx context.Context, db TxBeginner, request ReleaseDistrustRequest) error {
	if request.DistrustID == "" || request.SourceReleaseID == "" || request.SourceManifestRevision == 0 ||
		request.Reason == "" || !validDigest(request.EvaluatorArtifactDigest) || !validDigest(request.EvidenceDigest) ||
		(request.Scope != "MANIFEST" && request.Scope != "COMPATIBILITY_EDGE") {
		return errors.New("complete immutable release distrust evidence is required")
	}
	if request.Scope == "COMPATIBILITY_EDGE" && (request.TargetReleaseID == "" || request.TargetManifestRevision == 0) {
		return errors.New("compatibility edge distrust requires a target release")
	}
	if request.Scope == "MANIFEST" && (request.TargetReleaseID != "" || request.TargetManifestRevision != 0) {
		return errors.New("manifest distrust cannot include a target release")
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var targetID any
		var targetRevision any
		if request.Scope == "COMPATIBILITY_EDGE" {
			targetID, targetRevision = request.TargetReleaseID, request.TargetManifestRevision
		}
		command, err := tx.Exec(ctx, `INSERT INTO kim.release_distrust_evidence(
			distrust_id,distrust_scope,source_release_id,source_manifest_revision,target_release_id,
			target_manifest_revision,reason,evaluator_artifact_digest,evidence_digest
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT DO NOTHING`, request.DistrustID, request.Scope,
			request.SourceReleaseID, request.SourceManifestRevision, targetID, targetRevision, request.Reason,
			request.EvaluatorArtifactDigest, request.EvidenceDigest)
		if err != nil {
			return err
		}
		if command.RowsAffected() == 0 {
			var identical bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.release_distrust_evidence
				WHERE distrust_id=$1 AND distrust_scope=$2 AND source_release_id=$3 AND source_manifest_revision=$4
				 AND target_release_id IS NOT DISTINCT FROM $5 AND target_manifest_revision IS NOT DISTINCT FROM $6
				 AND reason=$7 AND evaluator_artifact_digest=$8 AND evidence_digest=$9)`, request.DistrustID,
				request.Scope, request.SourceReleaseID, request.SourceManifestRevision, targetID, targetRevision,
				request.Reason, request.EvaluatorArtifactDigest, request.EvidenceDigest).Scan(&identical); err != nil || !identical {
				return errors.New("release distrust evidence conflict")
			}
			return nil
		}
		if request.Scope == "MANIFEST" {
			if _, err := tx.Exec(ctx, `UPDATE kim.release_authority_current SET
				authority_generation=authority_generation+1,lifecycle_state='PAUSED',updated_at=statement_timestamp()
				WHERE singleton=true AND release_id=$1 AND manifest_revision=$2`,
				request.SourceReleaseID, request.SourceManifestRevision); err != nil {
				return err
			}
		}
		return fenceDistrustedReleaseBindings(ctx, tx, request)
	})
}

func fenceDistrustedReleaseBindings(ctx context.Context, tx pgx.Tx, request ReleaseDistrustRequest) error {
	var authorityID string
	var authorityRevision, authorityGeneration int64
	if err := tx.QueryRow(ctx, `SELECT release_id,manifest_revision,authority_generation
		FROM kim.release_authority_current WHERE singleton=true FOR SHARE`).Scan(&authorityID, &authorityRevision, &authorityGeneration); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT component_id,release_id,manifest_revision,artifact_digest,
		supported_work_schema_versions,binding_generation FROM kim.component_release_bindings_current
		WHERE release_id=$1 AND manifest_revision=$2 AND compatibility_state IN ('VALIDATED','COMPATIBLE')
		 AND ($3='MANIFEST' OR EXISTS(SELECT 1 FROM kim.compatibility_decision_evidence decision
			WHERE decision.decision_id=component_release_bindings_current.compatibility_decision_id
			 AND decision.target_release_id=$4 AND decision.target_manifest_revision=$5)) FOR UPDATE`,
		request.SourceReleaseID, request.SourceManifestRevision, request.Scope,
		request.TargetReleaseID, request.TargetManifestRevision)
	if err != nil {
		return err
	}
	type observedBinding struct {
		componentID, releaseID, artifact string
		revision, generation             int64
		schemas                          []string
	}
	var bindings []observedBinding
	for rows.Next() {
		var binding observedBinding
		if err := rows.Scan(&binding.componentID, &binding.releaseID, &binding.revision, &binding.artifact, &binding.schemas, &binding.generation); err != nil {
			rows.Close()
			return err
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, binding := range bindings {
		input := map[string]any{"subject": binding.componentID, "release": binding.releaseID, "revision": binding.revision,
			"artifact": binding.artifact, "schemas": binding.schemas, "target": authorityID,
			"target_revision": authorityRevision, "authority_generation": authorityGeneration,
			"decision": "INCOMPATIBLE", "reason": request.Reason, "evaluator": request.EvaluatorArtifactDigest,
			"distrust_id": request.DistrustID}
		raw, _ := json.Marshal(input)
		evidenceDigest := digestReleaseBytes(raw)
		decisionID := "compat:" + evidenceDigest
		if _, err := tx.Exec(ctx, `INSERT INTO kim.compatibility_decision_evidence(
			decision_id,subject_id,component_type,observed_release_id,observed_manifest_revision,
			observed_artifact_digest,supported_work_schema_versions,target_release_id,target_manifest_revision,
			release_authority_generation,decision,reason,evaluator_artifact_digest,evidence_digest
		) VALUES($1,$2,'OVN_RUNTIME_WORKER',$3,$4,$5,$6,$7,$8,$9,'INCOMPATIBLE',$10,$11,$12)`,
			decisionID, binding.componentID, binding.releaseID, binding.revision, binding.artifact, binding.schemas,
			authorityID, authorityRevision, authorityGeneration, request.Reason, request.EvaluatorArtifactDigest, evidenceDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.component_release_bindings_current SET
			binding_generation=binding_generation+1,compatibility_decision_id=$3,
			compatibility_state='INCOMPATIBLE',lifecycle_state='FENCED',updated_at=statement_timestamp()
			WHERE component_id=$1 AND binding_generation=$2`, binding.componentID, binding.generation, decisionID); err != nil {
			return err
		}
	}
	return nil
}

func SetComponentReleaseLifecycle(ctx context.Context, db TxBeginner, componentID string, generation uint64, state string) error {
	if componentID == "" || generation == 0 || (state != "DRAINING" && state != "STOPPED") {
		return errors.New("bounded component lifecycle transition is required")
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		command, err := tx.Exec(ctx, `UPDATE kim.component_release_bindings_current SET lifecycle_state=$3,updated_at=statement_timestamp()
			WHERE component_id=$1 AND binding_generation=$2 AND compatibility_state IN ('VALIDATED','COMPATIBLE')
			 AND ((lifecycle_state='ACTIVE' AND $3='DRAINING') OR (lifecycle_state='DRAINING' AND $3='STOPPED'))`, componentID, generation, state)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return ErrIncompatibleRelease
		}
		return nil
	})
}

func ActivateOVNWorkSchema(ctx context.Context, db TxBeginner, schema string) error {
	return switchOVNWorkSchema(ctx, db, schema, "ACTIVATE")
}

func RollbackOVNWorkSchema(ctx context.Context, db TxBeginner, schema string) error {
	return switchOVNWorkSchema(ctx, db, schema, "ROLLBACK")
}

func switchOVNWorkSchema(ctx context.Context, db TxBeginner, schema, transitionType string) error {
	if schema == "" {
		return ErrReleaseFeatureGateBlocked
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var releaseID, priorSchema string
		var revision, authorityGeneration, priorSchemaGeneration int64
		if err := tx.QueryRow(ctx, `SELECT release_id,manifest_revision,authority_generation,
			write_work_schema_version,write_schema_generation FROM kim.release_authority_current
			WHERE singleton=true FOR UPDATE`).Scan(&releaseID, &revision, &authorityGeneration, &priorSchema, &priorSchemaGeneration); err != nil {
			return err
		}
		if priorSchema == schema {
			return nil
		}
		var supported, blocked bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.release_manifest_component_evidence
			WHERE release_id=$1 AND manifest_revision=$2 AND component_type='OVN_RUNTIME_WORKER' AND $3=ANY(supported_work_schema_versions))`,
			releaseID, revision, schema).Scan(&supported); err != nil || !supported {
			return ErrReleaseFeatureGateBlocked
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.component_release_bindings_current
			WHERE lifecycle_state IN ('ACTIVE','DRAINING') AND compatibility_state IN ('VALIDATED','COMPATIBLE')
			 AND NOT ($1=ANY(supported_work_schema_versions)))`, schema).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			return ErrReleaseFeatureGateBlocked
		}
		newGeneration := priorSchemaGeneration + 1
		input := map[string]any{"release": releaseID, "revision": revision, "authority_generation": authorityGeneration,
			"prior_schema": priorSchema, "new_schema": schema, "prior_generation": priorSchemaGeneration,
			"new_generation": newGeneration, "transition_type": transitionType}
		raw, _ := json.Marshal(input)
		evidenceDigest := digestReleaseBytes(raw)
		transitionID := "schema-transition:" + evidenceDigest
		if _, err := tx.Exec(ctx, `INSERT INTO kim.release_work_schema_transition_evidence(
			transition_id,release_id,manifest_revision,release_authority_generation,prior_work_schema_version,
			new_work_schema_version,prior_schema_generation,new_schema_generation,transition_type,evidence_digest
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, transitionID, releaseID, revision, authorityGeneration,
			priorSchema, schema, priorSchemaGeneration, newGeneration, transitionType, evidenceDigest); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE kim.release_authority_current SET write_work_schema_version=$1,
			write_schema_generation=$2,updated_at=statement_timestamp() WHERE singleton=true`, schema, newGeneration)
		return err
	})
}

func canonicalSchemas(values []string) []string {
	copyValues := append([]string(nil), values...)
	slices.Sort(copyValues)
	copyValues = slices.Compact(copyValues)
	for _, value := range copyValues {
		if strings.TrimSpace(value) == "" {
			return nil
		}
	}
	return copyValues
}

func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func digestReleaseBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
