// Package migrations exposes immutable, checksummed KIM database migrations.
package migrations

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
)

//go:embed *.sql
var files embed.FS

var filenamePattern = regexp.MustCompile(`^(\d{3})_([a-z0-9_]+)\.sql$`)

// Migration is one immutable schema transition.
type Migration struct {
	Version  int64
	Name     string
	SQL      string
	Checksum string
}

// List returns migrations in strictly increasing version order.
func List() ([]Migration, error) {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	migrations := make([]Migration, 0, len(entries))
	seen := make(map[int64]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := filenamePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		version, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse migration version %q: %w", match[1], err)
		}
		if previous, ok := seen[version]; ok {
			return nil, fmt.Errorf("duplicate migration version %d in %q and %q", version, previous, entry.Name())
		}
		body, err := files.ReadFile(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		digest := sha256.Sum256(body)
		migrations = append(migrations, Migration{
			Version:  version,
			Name:     match[2],
			SQL:      string(body),
			Checksum: hex.EncodeToString(digest[:]),
		})
		seen[version] = entry.Name()
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	for index, migration := range migrations {
		expected := int64(index + 1)
		if migration.Version != expected {
			return nil, fmt.Errorf("migration sequence has version %d, want %d", migration.Version, expected)
		}
	}
	return migrations, nil
}
