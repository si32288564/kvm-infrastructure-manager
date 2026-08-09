package postgres

import "testing"

func TestQualificationFingerprintAndOperationContract(t *testing.T) {
	evidence := PCIQualificationEvidence{
		QualificationID: "qualification-1", Revision: 1, HostID: "host", DeviceAddress: "0000:03:00.1",
		ProfileRevision: "sriov-profile/v1", TestArtifactDigest: digestBytes([]byte("test")), EvaluatorDigest: digestBytes([]byte("evaluator")),
		ObservedGeneration: 7, ObservationDigest: digestBytes([]byte("observation")),
		BindingFingerprint:  map[string]string{"device": "8086:10ed", "driver": "ixgbevf/6", "firmware": "A", "kernel": "profile-k1", "iommu": "strict", "libvirt_qemu": "L1/Q1"},
		ValidatedOperations: []string{"VF_READ_BACK", "VF_ASSIGN"}, EvidenceState: "QUALIFIED",
	}
	_, firstDigest, operations, err := normalizeQualificationEvidence(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if operations[0] != "VF_ASSIGN" || operations[1] != "VF_READ_BACK" {
		t.Fatalf("operations = %#v", operations)
	}
	evidence.BindingFingerprint["driver"] = "ixgbevf/7"
	_, secondDigest, _, err := normalizeQualificationEvidence(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == secondDigest {
		t.Fatal("software stack change did not change qualification binding digest")
	}
	evidence.ValidatedOperations = []string{"VF_ASSIGN", "VF_ASSIGN"}
	if _, _, _, err := normalizeQualificationEvidence(evidence); err == nil {
		t.Fatal("duplicate validated operation was accepted")
	}
}
