// Package doclint validates cross-document contract references without external tooling.
package doclint

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	requirementDefinition = regexp.MustCompile(`(?m)^\| ((?:[A-Z]+-)+\d+) \|`)
	testContractID        = regexp.MustCompile(`\b(?:AT|FI|XCT)-[A-Z]+(?:-[A-Z]+)*-\d+\b`)
	testDefinition        = regexp.MustCompile(`(?m)^\| ((?:AT|FI|XCT)-[A-Z]+(?:-[A-Z]+)*-\d+) \|`)
	markdownLink          = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)
)

// Report summarizes the checked contract surface.
type Report struct {
	Requirements  int
	TestContracts int
	Links         int
}

// Check validates requirement traceability, test-contract references, and local links.
func Check(root string) (Report, error) {
	var report Report
	read := func(relative string) (string, error) {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			return "", fmt.Errorf("read %s: %w", relative, err)
		}
		return string(body), nil
	}

	requirements, err := read("docs/requirements.md")
	if err != nil {
		return report, err
	}
	traceability, err := read("docs/traceability-matrix.md")
	if err != nil {
		return report, err
	}
	invariants, err := read("docs/architecture-invariants.md")
	if err != nil {
		return report, err
	}

	var problems []string
	requirementIDs := uniqueMatches(requirementDefinition, requirements)
	for _, id := range requirementIDs {
		if !strings.Contains(traceability, id) {
			problems = append(problems, "requirement is not traced: "+id)
		}
	}
	report.Requirements = len(requirementIDs)

	markdownFiles, err := markdownFiles(root)
	if err != nil {
		return report, err
	}
	definedTests := make(map[string]struct{})
	for _, path := range markdownFiles {
		body, err := os.ReadFile(path)
		if err != nil {
			return report, fmt.Errorf("read %s: %w", path, err)
		}
		for _, id := range uniqueMatches(testDefinition, string(body)) {
			if _, duplicate := definedTests[id]; duplicate {
				problems = append(problems, "test contract is defined more than once: "+id)
			}
			definedTests[id] = struct{}{}
		}
	}
	report.TestContracts = len(definedTests)

	for _, source := range []struct {
		name string
		body string
	}{
		{name: "architecture-invariants.md", body: invariants},
		{name: "traceability-matrix.md", body: traceability},
	} {
		for _, id := range uniqueMatches(testContractID, source.body) {
			if _, ok := definedTests[id]; !ok {
				problems = append(problems, source.name+" references undefined test contract: "+id)
			}
		}
	}

	for _, path := range markdownFiles {
		body, err := os.ReadFile(path)
		if err != nil {
			return report, fmt.Errorf("read %s: %w", path, err)
		}
		for _, match := range markdownLink.FindAllStringSubmatch(string(body), -1) {
			target := strings.TrimSpace(match[1])
			if target == "" || strings.HasPrefix(target, "#") || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			target = strings.SplitN(target, "#", 2)[0]
			resolved := filepath.Clean(filepath.Join(filepath.Dir(path), filepath.FromSlash(target)))
			if _, err := os.Stat(resolved); err != nil {
				problems = append(problems, fmt.Sprintf("%s has broken local link %q", filepath.ToSlash(path), target))
			}
			report.Links++
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return report, errors.New(strings.Join(problems, "\n"))
	}
	return report, nil
}

func markdownFiles(root string) ([]string, error) {
	var files []string
	for _, base := range []string{filepath.Join(root, "README.md"), filepath.Join(root, "docs")} {
		info, err := os.Stat(base)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			files = append(files, base)
			continue
		}
		err = filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(path), ".md") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

func uniqueMatches(pattern *regexp.Regexp, text string) []string {
	seen := make(map[string]struct{})
	for _, match := range pattern.FindAllStringSubmatch(text, -1) {
		value := match[0]
		if len(match) > 1 {
			value = match[1]
		}
		seen[value] = struct{}{}
	}
	values := make([]string, 0, len(seen))
	for value := range seen {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}
