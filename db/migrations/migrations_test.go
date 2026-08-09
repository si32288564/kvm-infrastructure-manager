package migrations

import (
	"regexp"
	"testing"
)

func TestListIsContiguousAndChecksummed(t *testing.T) {
	migrations, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("List returned no migrations")
	}
	hexDigest := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for index, migration := range migrations {
		if migration.Version != int64(index+1) {
			t.Fatalf("migration[%d].Version = %d", index, migration.Version)
		}
		if migration.Name == "" || migration.SQL == "" || !hexDigest.MatchString(migration.Checksum) {
			t.Fatalf("migration %d is incomplete: %#v", migration.Version, migration)
		}
	}
}
