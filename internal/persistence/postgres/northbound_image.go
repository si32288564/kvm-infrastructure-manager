package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/execution/imageartifact"
	imageapi "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/image"
	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/resource"
)

type NorthboundImageStore struct{ DB TxBeginner }

func (s NorthboundImageStore) Create(ctx context.Context, p resource.Principal, r imageapi.CreateRequest, id, digest string) (out imageapi.Resource, replay bool, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return resource.ErrServiceUnavailable
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, `northbound-image-idempotency/`+p.Issuer+`/`+p.Subject+`/`+r.ProjectID+`/`+r.IdempotencyKey); err != nil {
			return err
		}
		allowed, err := authorizeNorthboundTx(ctx, tx, p, "CREATE", r.ProjectID)
		if err != nil {
			return err
		}
		if !allowed {
			returned = resource.ErrForbidden
			return auditTx(ctx, tx, p, r.RequestID, "IMAGE_CREATE", "IMAGE", "", 0, "PROJECT", r.ProjectID, "DENIED", "FORBIDDEN", digest)
		}
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.projects_current WHERE project_id=$1 AND lifecycle_state='ACTIVE')`, r.ProjectID).Scan(&active); err != nil {
			return err
		}
		if !active {
			returned = resource.ErrNotFound
			return nil
		}
		var oldDigest, oldID string
		var oldRevision uint64
		err = tx.QueryRow(ctx, `SELECT request_digest,image_id,image_revision FROM kim.northbound_image_idempotency_evidence WHERE principal_issuer=$1 AND principal_subject=$2 AND parent_project_id=$3 AND idempotency_key=$4`, p.Issuer, p.Subject, r.ProjectID, r.IdempotencyKey).Scan(&oldDigest, &oldID, &oldRevision)
		if err == nil {
			if oldDigest != digest {
				returned = resource.ErrIdempotencyConflict
				return nil
			}
			out, err = loadNorthboundImageTx(ctx, tx, oldID, false)
			replay = true
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := insertNorthboundImageRevisionTx(ctx, tx, id, 1, r.Desired, digest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.northbound_images_current(image_id,image_revision,owner_project_id,lifecycle_state,verification_state,authority_generation) VALUES($1,1,$2,'ACTIVE','PENDING',1)`, id, r.ProjectID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.northbound_image_idempotency_evidence(principal_issuer,principal_subject,parent_project_id,idempotency_key,request_digest,image_id,image_revision,request_id) VALUES($1,$2,$3,$4,$5,$6,1,$7)`, p.Issuer, p.Subject, r.ProjectID, r.IdempotencyKey, digest, id, r.RequestID); err != nil {
			return err
		}
		if err := auditTx(ctx, tx, p, r.RequestID, "IMAGE_CREATE", "IMAGE", id, 1, "PROJECT", r.ProjectID, "SUCCEEDED", "CREATED", digest); err != nil {
			return err
		}
		out, err = loadNorthboundImageTx(ctx, tx, id, false)
		return err
	})
	if err != nil {
		return out, false, fmt.Errorf("create Image authority: %w", err)
	}
	return out, replay, returned
}

func (s NorthboundImageStore) Get(ctx context.Context, p resource.Principal, id, requestID string) (out imageapi.Resource, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var owner string
		if err := tx.QueryRow(ctx, `SELECT owner_project_id FROM kim.northbound_images_current WHERE image_id=$1`, id).Scan(&owner); errors.Is(err, pgx.ErrNoRows) {
			returned = resource.ErrNotFound
			return nil
		} else if err != nil {
			return err
		}
		ok, err := authorizeNorthboundTx(ctx, tx, p, "READ", owner)
		if err != nil {
			return err
		}
		if !ok {
			returned = resource.ErrForbidden
			return nil
		}
		out, err = loadNorthboundImageTx(ctx, tx, id, false)
		return err
	})
	if err != nil {
		return out, fmt.Errorf("read Image authority: %w", err)
	}
	return out, returned
}
func (s NorthboundImageStore) List(ctx context.Context, p resource.Principal, r imageapi.ListRequest, requestID string) (page imageapi.Page, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT c.image_id,c.owner_project_id,e.image_name,e.architecture,e.image_format,e.expected_digest,e.source_id,e.visibility,c.verified_digest,c.verified_size_bytes,c.verification_state,c.lifecycle_state,e.delete_protection,c.image_revision,c.created_at,c.updated_at FROM kim.northbound_images_current c JOIN kim.northbound_image_revision_evidence e ON(e.image_id,e.image_revision)=(c.image_id,c.image_revision) WHERE c.lifecycle_state<>'DELETED' AND c.image_id>$3 AND ($4='' OR c.owner_project_id=$4) AND (EXISTS(SELECT 1 FROM kim.northbound_role_bindings_current b WHERE b.principal_issuer=$1 AND b.principal_subject=$2 AND b.lifecycle_state='ACTIVE' AND b.scope_type='SYSTEM' AND b.role IN('READER','WRITER','ADMIN')) OR EXISTS(SELECT 1 FROM kim.northbound_role_bindings_current b WHERE b.principal_issuer=$1 AND b.principal_subject=$2 AND b.lifecycle_state='ACTIVE' AND b.scope_type='PROJECT' AND b.scope_id=c.owner_project_id AND b.role IN('READER','WRITER','ADMIN'))) ORDER BY c.image_id LIMIT $5`, p.Issuer, p.Subject, r.AfterID, r.ProjectID, r.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x imageapi.Resource
			if err := rows.Scan(&x.ID, &x.ProjectID, &x.Name, &x.Architecture, &x.Format, &x.ExpectedDigest, &x.SourceID, &x.Visibility, &x.VerifiedDigest, &x.VerifiedSizeBytes, &x.VerificationState, &x.LifecycleState, &x.DeleteProtection, &x.Revision, &x.CreatedAt, &x.UpdatedAt); err != nil {
				return err
			}
			page.Items = append(page.Items, x)
		}
		if len(page.Items) > r.Limit {
			page.NextAfter = page.Items[r.Limit-1].ID
			page.Items = page.Items[:r.Limit]
		}
		return rows.Err()
	})
	if err != nil {
		return page, fmt.Errorf("list Images: %w", err)
	}
	return page, returned
}

func (s NorthboundImageStore) Patch(ctx context.Context, p resource.Principal, id string, expected uint64, patch imageapi.Patch, requestID string) (out imageapi.Resource, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, `image/`+id); err != nil {
			return err
		}
		current, err := loadNorthboundImageTx(ctx, tx, id, true)
		if errors.Is(err, pgx.ErrNoRows) {
			returned = resource.ErrNotFound
			return nil
		}
		if err != nil {
			return err
		}
		ok, err := authorizeNorthboundTx(ctx, tx, p, "UPDATE", current.ProjectID)
		if err != nil {
			return err
		}
		if !ok {
			returned = resource.ErrForbidden
			return nil
		}
		if current.Revision != expected {
			returned = resource.ErrStaleRevision
			return nil
		}
		desired := imageapi.Desired{ProjectID: current.ProjectID, Name: current.Name, Architecture: current.Architecture, Format: current.Format, ExpectedDigest: current.ExpectedDigest, SourceID: current.SourceID, Visibility: current.Visibility, DeleteProtection: current.DeleteProtection}
		if patch.Name != nil {
			desired.Name = *patch.Name
		}
		if patch.DeleteProtection != nil {
			desired.DeleteProtection = *patch.DeleteProtection
		}
		lifecycle := current.LifecycleState
		if patch.LifecycleState != nil {
			if *patch.LifecycleState != "ACTIVE" && *patch.LifecycleState != "DEPRECATED" {
				returned = resource.ErrValidation
				return nil
			}
			lifecycle = *patch.LifecycleState
		}
		digest, _ := imageapi.DesiredDigest(desired)
		if desired.Name == "" || len(desired.Name) > 255 {
			returned = resource.ErrValidation
			return nil
		}
		next := current.Revision + 1
		if err := insertNorthboundImageRevisionTx(ctx, tx, id, next, desired, digest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.northbound_images_current SET image_revision=$2,lifecycle_state=$3,authority_generation=authority_generation+1,updated_at=statement_timestamp() WHERE image_id=$1`, id, next, lifecycle); err != nil {
			return err
		}
		if current.VerificationState == "VERIFIED" {
			if err := publishVerifiedImageRevisionTx(ctx, tx, id, next, current.VerifiedDigest, current.VerifiedSizeBytes); err != nil {
				return err
			}
			if lifecycle == "DEPRECATED" {
				if _, err := tx.Exec(ctx, `UPDATE kim.images_current SET lifecycle_state='DELETING',updated_at=statement_timestamp() WHERE image_id=$1`, id); err != nil {
					return err
				}
			}
		}
		out, err = loadNorthboundImageTx(ctx, tx, id, false)
		return err
	})
	if err != nil {
		return out, fmt.Errorf("patch Image: %w", err)
	}
	return out, returned
}

func (s NorthboundImageStore) Delete(ctx context.Context, p resource.Principal, id string, expected uint64, requestID string) (returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var revision uint64
		var owner, state string
		var protected bool
		err := tx.QueryRow(ctx, `SELECT c.image_revision,c.owner_project_id,c.lifecycle_state,e.delete_protection FROM kim.northbound_images_current c JOIN kim.northbound_image_revision_evidence e ON(e.image_id,e.image_revision)=(c.image_id,c.image_revision) WHERE c.image_id=$1 FOR UPDATE OF c`, id).Scan(&revision, &owner, &state, &protected)
		if errors.Is(err, pgx.ErrNoRows) {
			returned = resource.ErrNotFound
			return nil
		}
		if err != nil {
			return err
		}
		ok, err := authorizeNorthboundTx(ctx, tx, p, "DELETE", owner)
		if err != nil {
			return err
		}
		if !ok {
			returned = resource.ErrForbidden
			return nil
		}
		if state == "DELETED" {
			return nil
		}
		if revision != expected {
			returned = resource.ErrStaleRevision
			return nil
		}
		if protected {
			returned = resource.ErrDeleteProtected
			return nil
		}
		var dependent bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kim.placement_admission_decisions WHERE image_id=$1) OR EXISTS(SELECT 1 FROM kim.vm_materialization_plan_evidence WHERE image_id=$1)`, id).Scan(&dependent); err != nil {
			return err
		}
		if dependent {
			returned = resource.ErrDependencyConflict
			return nil
		}
		_, err = tx.Exec(ctx, `UPDATE kim.northbound_images_current SET lifecycle_state='DELETED',deleted_from_revision=$2,authority_generation=authority_generation+1,updated_at=statement_timestamp() WHERE image_id=$1`, id, revision)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE kim.images_current SET lifecycle_state='DELETED',updated_at=statement_timestamp() WHERE image_id=$1`, id)
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("delete Image: %w", err)
	}
	return returned
}

func (s NorthboundImageStore) CreateIngestion(ctx context.Context, p resource.Principal, id string, revision uint64, r imageapi.IngestionRequest, operationID, requestDigest string) (out imageapi.Operation, replay bool, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, `image-ingestion/`+id); err != nil {
			return err
		}
		current, err := loadNorthboundImageTx(ctx, tx, id, true)
		if errors.Is(err, pgx.ErrNoRows) {
			returned = resource.ErrNotFound
			return nil
		}
		if err != nil {
			return err
		}
		ok, err := authorizeNorthboundTx(ctx, tx, p, "UPDATE", current.ProjectID)
		if err != nil {
			return err
		}
		if !ok {
			returned = resource.ErrForbidden
			return nil
		}
		if current.Revision != revision {
			returned = resource.ErrStaleRevision
			return nil
		}
		var existingID, existingDigest string
		err = tx.QueryRow(ctx, `SELECT operation_id,request_digest FROM kim.image_ingestion_operation_evidence WHERE principal_issuer=$1 AND principal_subject=$2 AND image_id=$3 AND image_revision=$4 AND idempotency_key=$5`, p.Issuer, p.Subject, id, revision, r.IdempotencyKey).Scan(&existingID, &existingDigest)
		if err == nil {
			if existingDigest != requestDigest {
				returned = resource.ErrIdempotencyConflict
				return nil
			}
			out, err = loadOperationTx(ctx, tx, existingID)
			replay = true
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var nextGen uint64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(artifact_generation),0)+1 FROM kim.image_ingestion_operation_evidence WHERE image_id=$1`, id).Scan(&nextGen); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.image_ingestion_operation_evidence(operation_id,image_id,image_revision,artifact_generation,source_id,expected_digest,principal_issuer,principal_subject,request_id,idempotency_key,request_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, operationID, id, revision, nextGen, current.SourceID, current.ExpectedDigest, p.Issuer, p.Subject, r.RequestID, r.IdempotencyKey, requestDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.image_ingestion_operations_current(operation_id,phase,retryable,cancellable) VALUES($1,'PENDING',true,false)`, operationID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.northbound_images_current SET verification_state='VERIFYING',verified_digest=NULL,verified_size_bytes=NULL,current_ingestion_operation_id=$2,authority_generation=authority_generation+1,updated_at=statement_timestamp() WHERE image_id=$1`, id, operationID); err != nil {
			return err
		}
		out, err = loadOperationTx(ctx, tx, operationID)
		return err
	})
	if err != nil {
		return out, false, fmt.Errorf("create Image ingestion: %w", err)
	}
	if out.Phase == "PENDING" {
		if _, authorizeErr := AuthorizeImageIngestionCommand(ctx, s.DB, out.ID); authorizeErr == nil {
			_ = pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
				var loadErr error
				out, loadErr = loadOperationTx(ctx, tx, out.ID)
				return loadErr
			})
		} else if !errors.Is(authorizeErr, resource.ErrServiceUnavailable) {
			return out, replay, fmt.Errorf("authorize Image ingestion: %w", authorizeErr)
		}
	}
	return out, replay, returned
}

func (s NorthboundImageStore) GetOperation(ctx context.Context, p resource.Principal, id, requestID string) (out imageapi.Operation, returned error) {
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var owner string
		if err := tx.QueryRow(ctx, `SELECT c.owner_project_id FROM kim.image_ingestion_operation_evidence o JOIN kim.northbound_images_current c USING(image_id) WHERE o.operation_id=$1`, id).Scan(&owner); errors.Is(err, pgx.ErrNoRows) {
			returned = resource.ErrNotFound
			return nil
		} else if err != nil {
			return err
		}
		ok, err := authorizeNorthboundTx(ctx, tx, p, "READ", owner)
		if err != nil {
			return err
		}
		if !ok {
			returned = resource.ErrForbidden
			return nil
		}
		out, err = loadOperationTx(ctx, tx, id)
		return err
	})
	if err != nil {
		return out, fmt.Errorf("read Image ingestion: %w", err)
	}
	return out, returned
}

func insertNorthboundImageRevisionTx(ctx context.Context, tx pgx.Tx, id string, revision uint64, d imageapi.Desired, digest string) error {
	_, err := tx.Exec(ctx, `INSERT INTO kim.northbound_image_revision_evidence(image_id,image_revision,owner_project_id,image_name,architecture,image_format,expected_digest,source_id,visibility,delete_protection,desired_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, id, revision, d.ProjectID, d.Name, d.Architecture, d.Format, d.ExpectedDigest, d.SourceID, d.Visibility, d.DeleteProtection, digest)
	return err
}
func loadNorthboundImageTx(ctx context.Context, tx pgx.Tx, id string, lock bool) (x imageapi.Resource, err error) {
	q := `SELECT c.image_id,c.owner_project_id,e.image_name,e.architecture,e.image_format,e.expected_digest,e.source_id,e.visibility,c.verified_digest,c.verified_size_bytes,c.verification_state,c.lifecycle_state,e.delete_protection,c.image_revision,c.created_at,c.updated_at FROM kim.northbound_images_current c JOIN kim.northbound_image_revision_evidence e ON(e.image_id,e.image_revision)=(c.image_id,c.image_revision) WHERE c.image_id=$1 AND c.lifecycle_state<>'DELETED'`
	if lock {
		q += ` FOR UPDATE OF c`
	}
	err = tx.QueryRow(ctx, q, id).Scan(&x.ID, &x.ProjectID, &x.Name, &x.Architecture, &x.Format, &x.ExpectedDigest, &x.SourceID, &x.Visibility, &x.VerifiedDigest, &x.VerifiedSizeBytes, &x.VerificationState, &x.LifecycleState, &x.DeleteProtection, &x.Revision, &x.CreatedAt, &x.UpdatedAt)
	return
}
func loadOperationTx(ctx context.Context, tx pgx.Tx, id string) (x imageapi.Operation, err error) {
	x.Type = "IMAGE_ARTIFACT_INGEST"
	x.TargetResourceType = "IMAGE"
	err = tx.QueryRow(ctx, `SELECT o.operation_id,o.image_id,o.image_revision,o.accepted_at,c.phase,c.terminal_state,c.error_code,c.retryable,c.cancellable,c.completed_at FROM kim.image_ingestion_operation_evidence o JOIN kim.image_ingestion_operations_current c USING(operation_id) WHERE o.operation_id=$1`, id).Scan(&x.ID, &x.TargetResourceID, &x.TargetRevision, &x.AcceptedAt, &x.Phase, &x.TerminalState, &x.ErrorCode, &x.Retryable, &x.Cancellable, &x.CompletedAt)
	return
}

func publishVerifiedImageRevisionTx(ctx context.Context, tx pgx.Tx, id string, revision uint64, digest *string, size *uint64) error {
	if digest == nil || size == nil {
		return resource.ErrConflict
	}
	var owner, format, source, visibility string
	var desiredDigest string
	if err := tx.QueryRow(ctx, `SELECT owner_project_id,image_format,source_id,visibility,desired_digest FROM kim.northbound_image_revision_evidence WHERE image_id=$1 AND image_revision=$2`, id, revision).Scan(&owner, &format, &source, &visibility, &desiredDigest); err != nil {
		return err
	}
	metadata := []byte(`{"architecture":"northbound"}`)
	m := sha256.Sum256(metadata)
	canonical := ImageRevision{ImageID: id, Revision: revision, OwnerProjectID: owner, Format: format, SizeBytes: *size, DeclaredChecksum: *digest, ObservedChecksum: *digest, SignatureState: "UNVERIFIED", SourceURI: "source-registry:" + source, Visibility: visibility, Metadata: map[string]string{"authority": "northbound-image-ingestion"}}
	metadataJSON, metadataDigest, err := canonicalCatalogMap(canonical.Metadata)
	if err != nil {
		return err
	}
	revisionDigest, err := imageRevisionDigest(canonical, metadataJSON)
	if err != nil {
		return err
	}
	_ = m
	_ = desiredDigest
	_, err = tx.Exec(ctx, `INSERT INTO kim.image_revision_evidence(image_id,image_revision,owner_project_id,image_format,size_bytes,checksum_algorithm,declared_checksum,observed_checksum,signature_state,source_uri,visibility,validation_state,validation_reason,metadata,metadata_digest,revision_digest) VALUES($1,$2,$3,$4,$5,'SHA256',$6,$6,'UNVERIFIED',$7,$8,'VERIFIED','independent_ingestion_digest_matched',$9,$10,$11) ON CONFLICT DO NOTHING`, id, revision, owner, format, *size, *digest, "source-registry:"+source, visibility, metadataJSON, metadataDigest, revisionDigest)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO kim.images_current(image_id,image_revision,owner_project_id,lifecycle_state,authority_generation) VALUES($1,$2,$3,'ACTIVE',1) ON CONFLICT(image_id) DO UPDATE SET image_revision=EXCLUDED.image_revision,owner_project_id=EXCLUDED.owner_project_id,lifecycle_state='ACTIVE',authority_generation=kim.images_current.authority_generation+1,updated_at=statement_timestamp() WHERE kim.images_current.image_revision<EXCLUDED.image_revision`, id, revision, owner)
	return err
}

type ImageIngestionObservation struct {
	OperationID, ObservationID, VerificationID string
}

type ImageIngestionCommand struct {
	OperationID, JobID, CommandID, HostID, PayloadDigest string
	HostAuthorityGeneration                              uint64
}

func AuthorizeImageIngestionCommand(ctx context.Context, db TxBeginner, operationID string) (out ImageIngestionCommand, returned error) {
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var imageID, sourceID, expected, phase string
		var revision, generation uint64
		if err := tx.QueryRow(ctx, `SELECT o.image_id,o.image_revision,o.artifact_generation,o.source_id,o.expected_digest,c.phase FROM kim.image_ingestion_operation_evidence o JOIN kim.image_ingestion_operations_current c USING(operation_id) WHERE o.operation_id=$1 FOR UPDATE OF c`, operationID).Scan(&imageID, &revision, &generation, &sourceID, &expected, &phase); err != nil {
			return err
		}
		if phase == "SUCCEEDED" || phase == "FAILED" {
			return resource.ErrConflict
		}
		var host string
		var authority uint64
		if err := tx.QueryRow(ctx, `SELECT a.host_id,a.authority_generation FROM kim.host_operation_authorities_current a JOIN kim.agent_transport_sessions_current session ON session.host_id=a.host_id AND session.session_generation=a.session_generation AND session.state='CURRENT' JOIN kim.agent_transport_session_attempts attempt ON attempt.host_id=session.host_id AND attempt.session_attempt_id=session.current_session_attempt_id WHERE a.authority_state='ARMED' AND attempt.handshake_evidence->'capabilities' ? $1 ORDER BY a.host_id LIMIT 1`, "kim.image-artifact-ingest/v1").Scan(&host, &authority); err != nil {
			return resource.ErrServiceUnavailable
		}
		jobID, commandID := "image-ingestion-job-"+operationID, "image-ingestion-command-"+operationID
		payload := map[string]any{"image_id": imageID, "image_revision": revision, "artifact_generation": generation, "source_id": sourceID, "expected_digest": expected, "maximum_bytes": uint64(16 * 1024 * 1024 * 1024 * 1024), "desired_state": "VERIFIED"}
		if err := CreateExecutionCommand(ctx, scopeTxBeginner{tx}, ExecutionCommandRequest{JobID: jobID, CommandID: commandID, HostID: host, ResourceType: "IMAGE_INGESTION_OPERATION", ResourceID: operationID, DesiredRevision: int64(generation), CommandType: imageartifact.CommandType, SchemaVersion: imageartifact.SchemaVersion, TargetResourceID: "image:" + imageID + ":" + fmt.Sprint(revision), Payload: payload}); err != nil {
			return err
		}
		raw, _ := json.Marshal(payload)
		sum := sha256.Sum256(raw)
		pd := hex.EncodeToString(sum[:])
		if _, err := tx.Exec(ctx, `INSERT INTO kim.image_ingestion_command_evidence(operation_id,job_id,command_id,host_id,host_authority_generation,command_payload_digest) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, operationID, jobID, commandID, host, authority, pd); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kim.image_ingestion_operations_current SET phase='RUNNING',updated_at=statement_timestamp() WHERE operation_id=$1`, operationID); err != nil {
			return err
		}
		out = ImageIngestionCommand{OperationID: operationID, JobID: jobID, CommandID: commandID, HostID: host, HostAuthorityGeneration: authority, PayloadDigest: pd}
		return nil
	})
	return out, err
}

func RecordImageIngestionObservation(ctx context.Context, db TxBeginner, v ImageIngestionObservation) error {
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var imageID, sourceID, expected, commandID, observed, artifactIdentity, verifier, readBack, response string
		var revision, generation, attempt, observationGeneration, size uint64
		if err := tx.QueryRow(ctx, `SELECT o.image_id,o.image_revision,o.artifact_generation,o.source_id,o.expected_digest,ce.command_id,verification.attempt_index,verification.observation_generation,verification.verifier_artifact_digest,COALESCE(verification.evidence_payload->>'observed_digest',verification.evidence_payload->'read_back'->>'observed_digest'),COALESCE(verification.evidence_payload->>'observed_size_bytes',verification.evidence_payload->'read_back'->>'observed_size_bytes')::bigint,COALESCE(verification.evidence_payload->>'read_back_state',verification.evidence_payload->'read_back'->>'read_back_state'),verification.observation_digest,CASE WHEN EXISTS(SELECT 1 FROM kim.command_results result WHERE result.command_id=ce.command_id AND result.attempt_index=verification.attempt_index) THEN 'RECEIVED' ELSE 'LOST' END FROM kim.image_ingestion_operation_evidence o JOIN kim.image_ingestion_command_evidence ce USING(operation_id) JOIN kim.command_verification_evidence verification ON verification.command_id=ce.command_id AND verification.verification_id=$2 WHERE o.operation_id=$1 AND verification.verification_state IN('MATCHED','CONFLICTING') FOR SHARE OF o`, v.OperationID, v.VerificationID).Scan(&imageID, &revision, &generation, &sourceID, &expected, &commandID, &attempt, &observationGeneration, &verifier, &observed, &size, &readBack, &artifactIdentity, &response); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.image_ingestion_attempt_evidence(operation_id,attempt_index,execution_identity,response_state) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, v.OperationID, attempt, artifactIdentity, response); err != nil {
			return err
		}
		body, _ := json.Marshal(v)
		ed := sha256.Sum256(body)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.image_artifact_observation_evidence(observation_id,operation_id,command_id,verification_id,attempt_index,image_id,image_revision,artifact_generation,source_id,observed_size_bytes,digest_algorithm,observed_digest,artifact_identity,observation_generation,verifier_artifact_digest,read_back_state,evidence_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'SHA256',$11,$12,$13,$14,$15,$16) ON CONFLICT DO NOTHING`, v.ObservationID, v.OperationID, commandID, v.VerificationID, attempt, imageID, revision, generation, sourceID, size, observed, artifactIdentity, observationGeneration, verifier, readBack, hex.EncodeToString(ed[:])); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE kim.image_ingestion_operations_current SET phase='VERIFYING',attempt_index=$2,retryable=true,updated_at=statement_timestamp() WHERE operation_id=$1 AND phase IN('PENDING','RUNNING','UNKNOWN','VERIFYING')`, v.OperationID, attempt)
		_ = expected
		return err
	})
}

func ConvergeImageIngestionCommand(ctx context.Context, db TxBeginner, commandID, verificationID string) error {
	var operationID string
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT operation_id FROM kim.image_ingestion_command_evidence WHERE command_id=$1`, commandID).Scan(&operationID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	observationID := "image-artifact-observation-" + operationID
	if err := RecordImageIngestionObservation(ctx, db, ImageIngestionObservation{OperationID: operationID, ObservationID: observationID, VerificationID: verificationID}); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	_, err = FinalizeImageIngestion(ctx, db, operationID, observationID, "image-artifact-verification-"+operationID, "image-ingestion-terminal-"+operationID)
	return err
}

func FinalizeImageIngestion(ctx context.Context, db TxBeginner, operationID, observationID, verificationID, terminalID string) (imageapi.Operation, error) {
	var out imageapi.Operation
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var existingTerminal, existingVerification string
		existingErr := tx.QueryRow(ctx, `SELECT terminal_id,verification_id FROM kim.image_ingestion_terminal_evidence WHERE operation_id=$1`, operationID).Scan(&existingTerminal, &existingVerification)
		if existingErr == nil {
			if existingTerminal != terminalID || existingVerification != verificationID {
				return resource.ErrConflict
			}
			var loadErr error
			out, loadErr = loadOperationTx(ctx, tx, operationID)
			return loadErr
		}
		if !errors.Is(existingErr, pgx.ErrNoRows) {
			return existingErr
		}
		var imageID, expected, observed, readback string
		var revision, size uint64
		if err := tx.QueryRow(ctx, `SELECT o.image_id,o.image_revision,o.expected_digest,e.observed_digest,e.observed_size_bytes,e.read_back_state FROM kim.image_ingestion_operation_evidence o JOIN kim.image_artifact_observation_evidence e ON e.operation_id=o.operation_id WHERE o.operation_id=$1 AND e.observation_id=$2 FOR UPDATE OF o`, operationID, observationID).Scan(&imageID, &revision, &expected, &observed, &size, &readback); err != nil {
			return err
		}
		state, reason := "REJECTED", "DIGEST_MISMATCH"
		if readback == "COMPLETE" && expected == observed {
			state, reason = "VERIFIED", "DIGEST_MATCHED"
		}
		vd := sha256.Sum256([]byte(operationID + observationID + expected + observed + state))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.image_artifact_verification_evidence(verification_id,operation_id,observation_id,expected_digest,observed_digest,verified_size_bytes,verification_state,reason_code,verification_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT DO NOTHING`, verificationID, operationID, observationID, expected, observed, size, state, reason, hex.EncodeToString(vd[:])); err != nil {
			return err
		}
		terminal := "FAILED"
		if state == "VERIFIED" {
			terminal = "SUCCEEDED"
		}
		td := sha256.Sum256([]byte(operationID + verificationID + terminal))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.image_ingestion_terminal_evidence(terminal_id,operation_id,verification_id,terminal_state,terminal_digest) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, terminalID, operationID, verificationID, terminal, hex.EncodeToString(td[:])); err != nil {
			return err
		}
		if terminal == "SUCCEEDED" {
			d, s := observed, size
			if _, err := tx.Exec(ctx, `UPDATE kim.northbound_images_current SET verification_state='VERIFIED',verified_digest=$3,verified_size_bytes=$4,authority_generation=authority_generation+1,updated_at=statement_timestamp() WHERE image_id=$1 AND image_revision=$2 AND current_ingestion_operation_id=$5`, imageID, revision, d, s, operationID); err != nil {
				return err
			}
			if err := publishVerifiedImageRevisionTx(ctx, tx, imageID, revision, &d, &s); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, `UPDATE kim.northbound_images_current SET verification_state='FAILED',authority_generation=authority_generation+1,updated_at=statement_timestamp() WHERE image_id=$1 AND image_revision=$2 AND current_ingestion_operation_id=$3`, imageID, revision, operationID); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `UPDATE kim.image_ingestion_operations_current SET phase=$2,terminal_state=$2,error_code=$3,retryable=false,completed_at=statement_timestamp(),updated_at=statement_timestamp() WHERE operation_id=$1`, operationID, terminal, func() any {
			if terminal == "FAILED" {
				return reason
			}
			return nil
		}())
		if err != nil {
			return err
		}
		out, err = loadOperationTx(ctx, tx, operationID)
		return err
	})
	return out, err
}
