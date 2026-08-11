package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

type upgradeSnapshotBinding struct {
	SnapshotID, SnapshotDigest, HostGroupID, WaveID, ComponentType string
	TargetReleaseID, TargetArtifactDigest, BindingDigest           string
	MembershipSetGeneration, TargetManifestRevision                uint64
}

type upgradeSnapshotTargetProvenance struct {
	SnapshotID, SnapshotDigest, HostGroupID, HostID string
	MembershipEvidenceDigest, ProvenanceDigest      string
	MembershipSetGeneration, MembershipGeneration   uint64
}

type UpgradeCampaignResumeRequest struct {
	CampaignID, ResumeID, Actor string
}

func loadUpgradeHostGroupSnapshotTargets(ctx context.Context, db TxBeginner, request UpgradeCampaignPlanRequest) (
	[]upgradeSnapshotBinding, []UpgradeTargetPlan, map[string]upgradeSnapshotTargetProvenance, error,
) {
	if len(request.HostGroupSnapshots) == 0 {
		return nil, nil, map[string]upgradeSnapshotTargetProvenance{}, nil
	}
	plans := append([]UpgradeHostGroupSnapshotPlan(nil), request.HostGroupSnapshots...)
	slices.SortFunc(plans, func(a, b UpgradeHostGroupSnapshotPlan) int {
		if a.WaveID != b.WaveID {
			return compareString(a.WaveID, b.WaveID)
		}
		return compareString(a.SnapshotID, b.SnapshotID)
	})
	bindings := make([]upgradeSnapshotBinding, 0, len(plans))
	targets := make([]UpgradeTargetPlan, 0)
	provenance := make(map[string]upgradeSnapshotTargetProvenance)
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		for _, plan := range plans {
			if plan.SnapshotID == "" || !validDigest(plan.SnapshotDigest) || plan.WaveID == "" ||
				plan.ComponentType != "HOST_AGENT" || plan.TargetReleaseID == "" ||
				plan.TargetManifestRevision == 0 || !validDigest(plan.TargetArtifactDigest) {
				return ErrUpgradeCampaignEvidenceConflict
			}
			var binding upgradeSnapshotBinding
			var purpose string
			var memberCount int
			if err := tx.QueryRow(ctx, `SELECT snapshot_id,snapshot_digest,host_group_id,membership_set_generation,
				purpose,member_count FROM kim.host_group_membership_snapshot_evidence
				WHERE snapshot_id=$1 AND snapshot_digest=$2`, plan.SnapshotID, plan.SnapshotDigest).Scan(
				&binding.SnapshotID, &binding.SnapshotDigest, &binding.HostGroupID,
				&binding.MembershipSetGeneration, &purpose, &memberCount); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return ErrUpgradeCampaignEvidenceConflict
				}
				return err
			}
			if purpose != "UPGRADE" || memberCount == 0 {
				return ErrUpgradeCampaignEvidenceConflict
			}
			binding.WaveID, binding.ComponentType = plan.WaveID, plan.ComponentType
			binding.TargetReleaseID, binding.TargetManifestRevision = plan.TargetReleaseID, plan.TargetManifestRevision
			binding.TargetArtifactDigest = plan.TargetArtifactDigest
			binding.BindingDigest = digestReleaseBytes([]byte(fmt.Sprintf("%s\n%d\n%s\n%s\n%s\n%s\n%d\n%s",
				request.CampaignID, request.PlanRevision, plan.WaveID, plan.SnapshotID, plan.SnapshotDigest,
				plan.TargetReleaseID, plan.TargetManifestRevision, plan.TargetArtifactDigest)))
			bindings = append(bindings, binding)
			rows, err := tx.Query(ctx, `SELECT host_id,membership_generation,membership_evidence_digest
				FROM kim.host_group_membership_snapshot_members WHERE snapshot_id=$1 AND host_group_id=$2
				ORDER BY host_id`, binding.SnapshotID, binding.HostGroupID)
			if err != nil {
				return err
			}
			seen := 0
			for rows.Next() {
				var hostID, memberDigest string
				var memberGeneration uint64
				if err := rows.Scan(&hostID, &memberGeneration, &memberDigest); err != nil {
					rows.Close()
					return err
				}
				targetID := "upgrade-host:" + digestReleaseBytes([]byte(fmt.Sprintf("%s\n%d\n%s\n%s\n%s",
					request.CampaignID, request.PlanRevision, plan.WaveID, plan.SnapshotID, hostID)))
				if _, duplicate := provenance[targetID]; duplicate {
					rows.Close()
					return ErrUpgradeCampaignEvidenceConflict
				}
				targets = append(targets, UpgradeTargetPlan{TargetID: targetID, WaveID: plan.WaveID,
					ComponentType: plan.ComponentType, ComponentID: hostID, TargetReleaseID: plan.TargetReleaseID,
					TargetManifestRevision: plan.TargetManifestRevision, TargetArtifactDigest: plan.TargetArtifactDigest})
				provenanceDigest := digestReleaseBytes([]byte(fmt.Sprintf("%s\n%s\n%s\n%d\n%s\n%d\n%s",
					targetID, plan.SnapshotID, plan.SnapshotDigest, binding.MembershipSetGeneration,
					hostID, memberGeneration, memberDigest)))
				provenance[targetID] = upgradeSnapshotTargetProvenance{SnapshotID: plan.SnapshotID,
					SnapshotDigest: plan.SnapshotDigest, HostGroupID: binding.HostGroupID, HostID: hostID,
					MembershipSetGeneration: binding.MembershipSetGeneration, MembershipGeneration: memberGeneration,
					MembershipEvidenceDigest: memberDigest, ProvenanceDigest: provenanceDigest}
				seen++
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
			if seen != memberCount {
				return ErrUpgradeCampaignEvidenceConflict
			}
		}
		return nil
	})
	return bindings, targets, provenance, err
}

func compareString(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// ResumeUpgradeCampaign explicitly resumes the existing immutable Plan/Wave
// and Target set. It never reads HostGroup current membership.
func ResumeUpgradeCampaign(ctx context.Context, db TxBeginner, request UpgradeCampaignResumeRequest) error {
	if request.CampaignID == "" || request.ResumeID == "" || request.Actor == "" {
		return ErrUpgradeCampaignEvidenceConflict
	}
	return pgx.BeginTxFunc(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := requireActiveDatabaseAuthority(ctx, tx); err != nil {
			return err
		}
		var state, waveID, waveType string
		var planRevision, campaignGeneration uint64
		var owner *string
		var expiry *time.Time
		if err := tx.QueryRow(ctx, `SELECT campaign.campaign_state,campaign.plan_revision,
			campaign.campaign_generation,campaign.current_wave_id,wave.wave_type,campaign.coordinator_owner,
			campaign.coordinator_claim_expires_at
			FROM kim.upgrade_campaigns_current campaign JOIN kim.upgrade_wave_evidence wave
			ON wave.campaign_id=campaign.campaign_id AND wave.plan_revision=campaign.plan_revision
			AND wave.wave_id=campaign.current_wave_id WHERE campaign.campaign_id=$1 FOR UPDATE OF campaign`,
			request.CampaignID).Scan(&state, &planRevision, &campaignGeneration, &waveID, &waveType, &owner, &expiry); err != nil {
			return err
		}
		resumedState := "ROLLING"
		if waveType == "CANARY" {
			resumedState = "CANARY"
		}
		digest := digestReleaseBytes([]byte(fmt.Sprintf("%s\n%d\n%d\n%s\n%s\n%s",
			request.CampaignID, planRevision, campaignGeneration, waveID, resumedState, request.Actor)))
		var recordedDigest string
		err := tx.QueryRow(ctx, `SELECT evidence_digest FROM kim.upgrade_campaign_resume_evidence WHERE resume_id=$1`,
			request.ResumeID).Scan(&recordedDigest)
		if err == nil {
			if recordedDigest == digest {
				return nil
			}
			return ErrUpgradeCampaignEvidenceConflict
		}
		var now time.Time
		if nowErr := tx.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); nowErr != nil {
			return nowErr
		}
		if !errors.Is(err, pgx.ErrNoRows) || state != "PAUSED" || (owner != nil && expiry != nil && expiry.After(now)) {
			return ErrUpgradeCampaignEvidenceConflict
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kim.upgrade_campaign_resume_evidence(
			resume_id,campaign_id,plan_revision,campaign_generation,wave_id,resumed_state,actor,evidence_digest
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, request.ResumeID, request.CampaignID, planRevision,
			campaignGeneration, waveID, resumedState, request.Actor, digest); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE kim.upgrade_campaigns_current SET campaign_state=$2,
			coordinator_owner=NULL,coordinator_claim_expires_at=NULL,coordinator_maximum_expires_at=NULL,
			latest_canary_decision_id=NULL,updated_at=statement_timestamp() WHERE campaign_id=$1`,
			request.CampaignID, resumedState)
		return err
	})
}
