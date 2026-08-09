package inventory

import "github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/agent/session"

func NewEnvelope(snapshot Snapshot, sessionGeneration uint64, messageID string) (session.Envelope, error) {
	payload, err := snapshot.MarshalCanonical()
	if err != nil {
		return session.Envelope{}, err
	}
	envelope := session.NewEnvelope(snapshot.HostIdentity, sessionGeneration, session.StreamInventory, messageID, SnapshotSchemaV2, "host-inventory", snapshot.ObservationGeneration, payload)
	envelope.ResourceGeneration = snapshot.ObservationGeneration
	return envelope, nil
}
