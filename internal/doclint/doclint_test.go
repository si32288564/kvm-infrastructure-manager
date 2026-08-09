package doclint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckRepositoryContracts(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	report, err := Check(root)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if report.Requirements == 0 || report.TestContracts == 0 || report.Links == 0 {
		t.Fatalf("incomplete report: %#v", report)
	}
}

func TestCheckRejectsUntracedRequirement(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"README.md":                       "[Requirements](docs/requirements.md)\n",
		"docs/requirements.md":            "| FOO-001 | missing trace | Must |\n",
		"docs/traceability-matrix.md":     "# Trace\n",
		"docs/architecture-invariants.md": "# Invariants\n",
		"docs/acceptance-test-catalog.md": "| AT-FOO-001 | test |\n",
	}
	for path, body := range files {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(path)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Check(root); err == nil {
		t.Fatal("Check accepted an untraced requirement")
	}
}
