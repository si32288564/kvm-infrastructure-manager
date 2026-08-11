package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrFailureEpochConflict    = errors.New("Failure Epoch authority conflict")
	ErrFailureEpochStale       = errors.New("Failure Epoch source authority is stale")
	ErrFailureEvidenceConflict = errors.New("Failure Evidence identity conflict")
)

type FailureObservation struct {
	EvidenceID, FailureEpochID, EvidenceType, SourceType, SourceHostID string
	ObservedState, FreshnessState, PayloadDigest, EvidenceDigest       string
	EvidenceGeneration, SourceSessionGeneration                        uint64
	SourceCredentialBindingRevision, SourceHostAuthorityGeneration     uint64
	ObservationGeneration                                              uint64
	ObservedAt                                                         time.Time
}

type OpenFailureEpochRequest struct {
	OpenRequestID, FailureEpochID, IncidentKey, WorkloadID string
	FailureClass, RequestedBy, RequestDigest               string
	ExpectedBindingRevision                                uint64
	ExpectedBindingDigest                                  string
	Trigger                                                FailureObservation
}

type FailureEpoch struct {
	FailureEpochID, OpenRequestID, IncidentKey, SubjectType string
	WorkloadID, SourceHostID, FailureClass                  string
	AvailabilityBindingDigest, PolicyID, PolicyDigest       string
	AdmissionID, AllocationID, TriggeringEvidenceID         string
	EpochDigest, EpochState, SourceHostAuthorityState       string
	EpochGeneration, AvailabilityBindingRevision            uint64
	PolicyRevision, SourceHostAuthorityGeneration           uint64
	SourceSessionGeneration, TransitionGeneration           uint64
	LatestEvidenceGeneration                                uint64
}

type failureEpochRequestDigest struct {
	OpenRequestID, FailureEpochID, IncidentKey, WorkloadID string
	FailureClass, RequestedBy                              string
	ExpectedBindingRevision                                uint64
	ExpectedBindingDigest, TriggerDigest                   string
}

func validFailureObservation(o FailureObservation) bool {
	if o.EvidenceID == "" || o.SourceHostID == "" || o.ObservationGeneration == 0 || o.PayloadDigest == "" || o.ObservedAt.IsZero() {
		return false
	}
	if o.ObservedState != "PRESENT" && o.ObservedState != "ABSENT" && o.ObservedState != "UNKNOWN" && o.ObservedState != "CONFLICTING" {
		return false
	}
	if o.FreshnessState != "CURRENT" && o.FreshnessState != "STALE" && o.FreshnessState != "UNKNOWN" {
		return false
	}
	switch o.EvidenceType {
	case "AGENT_CONNECTIVITY_LOSS":
		return o.SourceType == "CONTROL_PLANE" && o.SourceSessionGeneration > 0 && o.SourceCredentialBindingRevision > 0
	case "HOST_OPERATION_AUTHORITY_STATE":
		return o.SourceType == "CONTROL_PLANE" && o.SourceHostAuthorityGeneration > 0
	case "VM_RUNTIME_OBSERVATION":
		return o.SourceType == "LIBVIRT_READ_BACK"
	default:
		return false
	}
}

func failureObservationDigestValue(o FailureObservation) string {
	o.EvidenceDigest = ""
	o.EvidenceGeneration = 0
	o.FailureEpochID = ""
	raw, _ := json.Marshal(o)
	return digestReleaseBytes(raw)
}

func failureEpochRequestDigestValue(r OpenFailureEpochRequest) string {
	raw, _ := json.Marshal(failureEpochRequestDigest{OpenRequestID: r.OpenRequestID, FailureEpochID: r.FailureEpochID,
		IncidentKey: r.IncidentKey, WorkloadID: r.WorkloadID, FailureClass: r.FailureClass, RequestedBy: r.RequestedBy,
		ExpectedBindingRevision: r.ExpectedBindingRevision, ExpectedBindingDigest: r.ExpectedBindingDigest,
		TriggerDigest: failureObservationDigestValue(r.Trigger)})
	return digestReleaseBytes(raw)
}

func validOpenFailureEpochRequest(r OpenFailureEpochRequest) bool {
	if r.OpenRequestID == "" || r.FailureEpochID == "" || r.IncidentKey == "" || r.WorkloadID == "" || r.RequestedBy == "" ||
		r.ExpectedBindingRevision == 0 || r.ExpectedBindingDigest == "" || !validFailureObservation(r.Trigger) {
		return false
	}
	return (r.FailureClass == "HOST_CONNECTIVITY_LOSS" && r.Trigger.EvidenceType == "AGENT_CONNECTIVITY_LOSS") ||
		(r.FailureClass == "HOST_AUTHORITY_LOSS" && r.Trigger.EvidenceType == "HOST_OPERATION_AUTHORITY_STATE") ||
		(r.FailureClass == "VM_RUNTIME_UNAVAILABLE" && r.Trigger.EvidenceType == "VM_RUNTIME_OBSERVATION")
}

func validateFailureObservationSourceTx(ctx context.Context, tx pgx.Tx, o FailureObservation, sourceHost, workloadID string) error {
	if o.SourceHostID != sourceHost {
		return ErrFailureEpochConflict
	}
	switch o.EvidenceType {
	case "AGENT_CONNECTIVITY_LOSS":
		var generation, credential uint64
		if err := tx.QueryRow(ctx, `SELECT session_generation,credential_binding_revision FROM kim.agent_transport_sessions_current WHERE host_id=$1 FOR SHARE`, sourceHost).Scan(&generation, &credential); err != nil || generation != o.SourceSessionGeneration || credential != o.SourceCredentialBindingRevision {
			return ErrFailureEpochStale
		}
	case "HOST_OPERATION_AUTHORITY_STATE":
		var generation uint64
		if err := tx.QueryRow(ctx, `SELECT authority_generation FROM kim.host_operation_authorities_current WHERE host_id=$1 FOR SHARE`, sourceHost).Scan(&generation); err != nil || generation != o.SourceHostAuthorityGeneration {
			return ErrFailureEpochStale
		}
	case "VM_RUNTIME_OBSERVATION":
		var generation uint64
		if err := tx.QueryRow(ctx, `SELECT p.observation_generation FROM kim.virtual_machines_current v JOIN kim.vm_power_state_current p ON p.vm_id=v.vm_id WHERE v.workload_id=$1 AND v.host_id=$2 FOR SHARE OF v,p`, workloadID, sourceHost).Scan(&generation); err != nil || generation != o.ObservationGeneration {
			return ErrFailureEpochStale
		}
	default:
		return ErrFailureEpochConflict
	}
	return nil
}

func loadFailureEpochTx(ctx context.Context, tx pgx.Tx, epochID string) (FailureEpoch, error) {
	var e FailureEpoch
	err := tx.QueryRow(ctx, `SELECT e.failure_epoch_id,e.open_request_id,e.incident_key,e.subject_type,e.workload_id,e.source_host_id,
		e.failure_class,e.availability_binding_revision,e.availability_binding_digest,e.availability_policy_id,
		e.availability_policy_revision,e.availability_policy_digest,e.admission_id,e.allocation_id,
		COALESCE(e.source_host_authority_generation,0),COALESCE(e.source_host_authority_state,'UNKNOWN'),
		COALESCE(e.source_session_generation,0),e.triggering_evidence_id,e.epoch_digest,c.epoch_state,
		c.epoch_generation,c.transition_generation,c.latest_evidence_generation
		FROM kim.failure_epoch_evidence e JOIN kim.failure_epochs_current c ON c.failure_epoch_id=e.failure_epoch_id
		WHERE e.failure_epoch_id=$1`, epochID).Scan(&e.FailureEpochID, &e.OpenRequestID, &e.IncidentKey, &e.SubjectType,
		&e.WorkloadID, &e.SourceHostID, &e.FailureClass, &e.AvailabilityBindingRevision, &e.AvailabilityBindingDigest,
		&e.PolicyID, &e.PolicyRevision, &e.PolicyDigest, &e.AdmissionID, &e.AllocationID, &e.SourceHostAuthorityGeneration,
		&e.SourceHostAuthorityState, &e.SourceSessionGeneration, &e.TriggeringEvidenceID, &e.EpochDigest, &e.EpochState,
		&e.EpochGeneration, &e.TransitionGeneration, &e.LatestEvidenceGeneration)
	return e, err
}

// OpenFailureEpoch atomically records the request, exact Binding/Policy-bound
// epoch, triggering observation, initial SUSPECTED transition, and projection.
func OpenFailureEpoch(ctx context.Context, db TxBeginner, request OpenFailureEpochRequest) (FailureEpoch, error) {
	if !validOpenFailureEpochRequest(request) {
		return FailureEpoch{}, ErrFailureEpochConflict
	}
	request.RequestDigest = failureEpochRequestDigestValue(request)
	var epoch FailureEpoch
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "failure-open/"+request.OpenRequestID); err != nil {
			return err
		}
		var existingDigest, existingEpoch string
		err := tx.QueryRow(ctx, `SELECT request_digest,failure_epoch_id FROM kim.failure_epoch_open_request_evidence WHERE open_request_id=$1`, request.OpenRequestID).Scan(&existingDigest, &existingEpoch)
		if err == nil {
			if existingDigest != request.RequestDigest {
				return ErrFailureEpochConflict
			}
			var loadErr error
			epoch, loadErr = loadFailureEpochTx(ctx, tx, existingEpoch)
			return loadErr
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "failure-incident/"+request.WorkloadID+"/"+request.FailureClass+"/"+request.IncidentKey); err != nil {
			return err
		}
		var correlatedID string
		err = tx.QueryRow(ctx, `SELECT failure_epoch_id FROM kim.failure_epoch_evidence WHERE workload_id=$1 AND failure_class=$2 AND incident_key=$3`, request.WorkloadID, request.FailureClass, request.IncidentKey).Scan(&correlatedID)
		if err == nil {
			epoch, err = loadFailureEpochTx(ctx, tx, correlatedID)
			if err != nil {
				return err
			}
			if epoch.AvailabilityBindingRevision != request.ExpectedBindingRevision || epoch.AvailabilityBindingDigest != request.ExpectedBindingDigest {
				return ErrFailureEpochConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "availability-binding/"+request.WorkloadID); err != nil {
			return err
		}
		var currentRevision uint64
		var currentDigest string
		if err := tx.QueryRow(ctx, `SELECT binding_revision,binding_digest FROM kim.vm_availability_bindings_current WHERE workload_id=$1 FOR SHARE`, request.WorkloadID).Scan(&currentRevision, &currentDigest); err != nil || currentRevision != request.ExpectedBindingRevision || currentDigest != request.ExpectedBindingDigest {
			return ErrFailureEpochStale
		}
		binding, err := loadVMAvailabilityBindingRevisionTx(ctx, tx, request.WorkloadID, currentRevision)
		if err != nil {
			return err
		}
		var hostID, admissionWorkload, allocationWorkload string
		if err := tx.QueryRow(ctx, `SELECT d.host_id,d.workload_id,c.workload_id FROM kim.placement_admission_decisions d JOIN kim.compute_allocation_claims c ON c.allocation_id=$2 AND c.admission_id=d.admission_id WHERE d.admission_id=$1`, binding.AdmissionID, binding.AllocationID).Scan(&hostID, &admissionWorkload, &allocationWorkload); err != nil || admissionWorkload != request.WorkloadID || allocationWorkload != request.WorkloadID {
			return ErrFailureEpochConflict
		}
		if err := validateFailureObservationSourceTx(ctx, tx, request.Trigger, hostID, request.WorkloadID); err != nil {
			return err
		}
		var authorityGeneration, sessionGeneration uint64
		authorityState := "UNKNOWN"
		_ = tx.QueryRow(ctx, `SELECT authority_generation,authority_state FROM kim.host_operation_authorities_current WHERE host_id=$1 FOR SHARE`, hostID).Scan(&authorityGeneration, &authorityState)
		_ = tx.QueryRow(ctx, `SELECT session_generation FROM kim.agent_transport_sessions_current WHERE host_id=$1 FOR SHARE`, hostID).Scan(&sessionGeneration)
		request.Trigger.FailureEpochID = request.FailureEpochID
		request.Trigger.EvidenceGeneration = 1
		request.Trigger.EvidenceDigest = failureObservationDigestValue(request.Trigger)
		epoch = FailureEpoch{FailureEpochID: request.FailureEpochID, OpenRequestID: request.OpenRequestID, IncidentKey: request.IncidentKey,
			SubjectType: "VIRTUAL_MACHINE", WorkloadID: request.WorkloadID, SourceHostID: hostID, FailureClass: request.FailureClass,
			AvailabilityBindingRevision: binding.BindingRevision, AvailabilityBindingDigest: binding.BindingDigest, PolicyID: binding.PolicyID,
			PolicyRevision: binding.PolicyRevision, PolicyDigest: binding.PolicyDigest, AdmissionID: binding.AdmissionID, AllocationID: binding.AllocationID,
			SourceHostAuthorityGeneration: authorityGeneration, SourceHostAuthorityState: authorityState, SourceSessionGeneration: sessionGeneration,
			TriggeringEvidenceID: request.Trigger.EvidenceID, EpochGeneration: 1, EpochState: "SUSPECTED", TransitionGeneration: 1, LatestEvidenceGeneration: 1}
		raw, _ := json.Marshal(epoch)
		epoch.EpochDigest = digestReleaseBytes(raw)
		if _, err := tx.Exec(ctx, `INSERT INTO kim.failure_epoch_open_request_evidence(open_request_id,failure_epoch_id,incident_key,workload_id,subject_type,failure_class,expected_binding_revision,expected_binding_digest,triggering_evidence_id,requested_by,request_digest) VALUES($1,$2,$3,$4,'VIRTUAL_MACHINE',$5,$6,$7,$8,$9,$10)`, request.OpenRequestID, request.FailureEpochID, request.IncidentKey, request.WorkloadID, request.FailureClass, request.ExpectedBindingRevision, request.ExpectedBindingDigest, request.Trigger.EvidenceID, request.RequestedBy, request.RequestDigest); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.failure_epoch_evidence(failure_epoch_id,open_request_id,incident_key,epoch_generation,subject_type,workload_id,source_host_id,failure_class,availability_binding_revision,availability_binding_digest,availability_policy_id,availability_policy_revision,availability_policy_digest,admission_id,allocation_id,source_host_authority_generation,source_host_authority_state,source_session_generation,triggering_evidence_id,epoch_digest) VALUES($1,$2,$3,1,'VIRTUAL_MACHINE',$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,NULLIF($14,0),$15,NULLIF($16,0),$17,$18)`, epoch.FailureEpochID, epoch.OpenRequestID, epoch.IncidentKey, epoch.WorkloadID, epoch.SourceHostID, epoch.FailureClass, epoch.AvailabilityBindingRevision, epoch.AvailabilityBindingDigest, epoch.PolicyID, epoch.PolicyRevision, epoch.PolicyDigest, epoch.AdmissionID, epoch.AllocationID, epoch.SourceHostAuthorityGeneration, epoch.SourceHostAuthorityState, epoch.SourceSessionGeneration, epoch.TriggeringEvidenceID, epoch.EpochDigest); err != nil {
			return err
		}
		if err := insertFailureObservationTx(ctx, tx, request.Trigger); err != nil {
			return err
		}
		transitionDigest := digestReleaseBytes([]byte(epoch.FailureEpochID + "/1/SUSPECTED/" + request.Trigger.EvidenceDigest))
		if _, err := tx.Exec(ctx, `INSERT INTO kim.failure_epoch_transition_evidence(failure_epoch_id,transition_generation,from_state,to_state,cause_evidence_id,transition_digest) VALUES($1,1,NULL,'SUSPECTED',$2,$3)`, epoch.FailureEpochID, request.Trigger.EvidenceID, transitionDigest); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO kim.failure_epochs_current(failure_epoch_id,epoch_generation,workload_id,failure_class,epoch_state,transition_generation,latest_evidence_generation) VALUES($1,1,$2,$3,'SUSPECTED',1,1)`, epoch.FailureEpochID, epoch.WorkloadID, epoch.FailureClass)
		return err
	})
	return epoch, err
}

func insertFailureObservationTx(ctx context.Context, tx pgx.Tx, o FailureObservation) error {
	_, err := tx.Exec(ctx, `INSERT INTO kim.failure_observation_evidence(evidence_id,failure_epoch_id,evidence_generation,evidence_type,source_type,source_host_id,source_session_generation,source_credential_binding_revision,source_host_authority_generation,observation_generation,observed_state,freshness_state,observed_at,payload_digest,evidence_digest) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,0),NULLIF($8,0),NULLIF($9,0),$10,$11,$12,$13,$14,$15)`, o.EvidenceID, o.FailureEpochID, o.EvidenceGeneration, o.EvidenceType, o.SourceType, o.SourceHostID, o.SourceSessionGeneration, o.SourceCredentialBindingRevision, o.SourceHostAuthorityGeneration, o.ObservationGeneration, o.ObservedState, o.FreshnessState, o.ObservedAt, o.PayloadDigest, o.EvidenceDigest)
	return err
}

// AppendFailureObservation preserves late/UNKNOWN evidence without changing
// the SUSPECTED transition or issuing confirmation, fencing, or recovery.
func AppendFailureObservation(ctx context.Context, db TxBeginner, epochID string, o FailureObservation) (FailureObservation, error) {
	if epochID == "" || !validFailureObservation(o) {
		return FailureObservation{}, ErrFailureEvidenceConflict
	}
	o.FailureEpochID = epochID
	o.EvidenceDigest = failureObservationDigestValue(o)
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "failure-evidence/"+o.EvidenceID); err != nil {
			return err
		}
		var existing string
		var generation uint64
		err := tx.QueryRow(ctx, `SELECT evidence_digest,evidence_generation FROM kim.failure_observation_evidence WHERE evidence_id=$1`, o.EvidenceID).Scan(&existing, &generation)
		if err == nil {
			if existing != o.EvidenceDigest {
				return ErrFailureEvidenceConflict
			}
			o.EvidenceGeneration = generation
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "failure-epoch/"+epochID); err != nil {
			return err
		}
		epoch, err := loadFailureEpochTx(ctx, tx, epochID)
		if err != nil {
			return err
		}
		if err := validateFailureObservationSourceTx(ctx, tx, o, epoch.SourceHostID, epoch.WorkloadID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(evidence_generation),0)+1 FROM kim.failure_observation_evidence WHERE failure_epoch_id=$1`, epochID).Scan(&o.EvidenceGeneration); err != nil {
			return err
		}
		if err := insertFailureObservationTx(ctx, tx, o); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE kim.failure_epochs_current SET latest_evidence_generation=$2,updated_at=statement_timestamp() WHERE failure_epoch_id=$1`, epochID, o.EvidenceGeneration)
		return err
	})
	return o, err
}
