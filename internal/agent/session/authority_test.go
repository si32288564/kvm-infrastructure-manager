package session

import "testing"

func TestAuthorityViewRejectsRollbackAndSameGenerationRearm(t *testing.T) {
	view, err := NewMemoryAuthorityView("host-1", 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := view.Apply(AuthoritySnapshot{HostIdentity: "host-1", SessionGeneration: 4, State: AuthorityFenced}); err != nil {
		t.Fatal(err)
	}
	if err := view.Apply(AuthoritySnapshot{HostIdentity: "host-1", SessionGeneration: 4, State: AuthorityCurrent}); err == nil {
		t.Fatal("same-generation rearm was accepted")
	}
	if err := view.Apply(AuthoritySnapshot{HostIdentity: "host-1", SessionGeneration: 3, State: AuthorityFenced}); err == nil {
		t.Fatal("generation rollback was accepted")
	}
	if err := view.Apply(AuthoritySnapshot{HostIdentity: "host-1", SessionGeneration: 5, State: AuthorityCurrent}); err != nil {
		t.Fatalf("higher-generation current authority was rejected: %v", err)
	}
}
