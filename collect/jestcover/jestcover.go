// Package jestcover collects PER-SPEC-FILE coverage for a Jest
// project — the collection half of the jest RunnerAdapter. Istanbul has no
// per-test contexts, so the collector runs each spec file in its own
// `jest --coverage` invocation; identity is the spec file (see the jest
// adapter's package comment for why file granularity is the right unit in
// JS).
//
// Output: the jest adapter's coverage JSON (specroster/jest-cover/v1) and
// a collected list of repo-relative spec paths.
//
// Usage:
//
//	specroster-jestcover [-dir .] [-repo-root <dir>] \
//	    [-o coverage.json] [-collected collected.txt]
package jestcover

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SpecRoster/Collector/covtypes"
	"github.com/SpecRoster/Collector/runner/jest"
)

// Run collects per-spec-file coverage for the Jest project at dir. An empty
// repoRoot defaults to dir.
func Run(dir, repoRoot, out, collected string) error {
	return run(dir, repoRoot, out, collected)
}

func run(dir, repoRoot, outPath, collectedPath string) error {
	if repoRoot == "" {
		repoRoot = dir
	}
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return err
	}

	specs, err := listSpecs(dir)
	if err != nil {
		return err
	}
	if len(specs) == 0 {
		return fmt.Errorf("jest discovered no test files in %s", dir)
	}

	layout := jest.Adapter{}.Layout("", "")
	doc := jest.CoverJSON{Format: jest.CoverageFormat, Tests: map[string]map[string][]int{}}
	var natives []string
	for _, absSpec := range specs {
		relSpec, err := filepath.Rel(absRoot, absSpec)
		if err != nil || strings.HasPrefix(relSpec, "..") {
			continue
		}
		relSpec = filepath.ToSlash(relSpec)
		natives = append(natives, relSpec)
		files, err := coverOneSpec(dir, absSpec, absRoot, layout)
		if err != nil {
			return fmt.Errorf("cover %s: %w", relSpec, err)
		}
		if len(files) > 0 {
			doc.Tests[relSpec] = files
		}
		fmt.Fprintf(os.Stderr, "covered %s (%d files)\n", relSpec, len(files))
	}

	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(doc); err != nil {
		return err
	}
	sort.Strings(natives)
	return os.WriteFile(collectedPath, []byte(strings.Join(natives, "\n")+"\n"), 0o644)
}

// listSpecs asks jest itself for the test files it would run.
func listSpecs(dir string) ([]string, error) {
	cmd := exec.Command("npx", "--no-install", "jest", "--listTests")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("jest --listTests: %w\n%s", err, ee.Stderr)
		}
		return nil, err
	}
	var specs []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" && filepath.IsAbs(l) {
			specs = append(specs, l)
		}
	}
	sort.Strings(specs)
	return specs, nil
}

// coverOneSpec runs one spec file with Istanbul JSON coverage and returns
// repo-relative source file → covered lines. Spec/test files themselves are
// excluded (identity tracks specs; the index maps product source only).
func coverOneSpec(dir, absSpec, absRoot string, layout covtypes.Layout) (map[string][]int, error) {
	tmp, err := os.MkdirTemp("", "trjest-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	cmd := exec.Command("npx", "--no-install", "jest", "--runTestsByPath", absSpec,
		"--coverage", "--coverageReporters=json", "--coverageDirectory="+tmp,
		"--ci", "--watchman=false", "--silent")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("jest run: %w\n%s", err, out)
	}
	return parseIstanbul(filepath.Join(tmp, "coverage-final.json"), absRoot, layout)
}

// istanbulFile is the per-file slice of Istanbul's coverage-final.json.
type istanbulFile struct {
	StatementMap map[string]struct {
		Start struct {
			Line int `json:"line"`
		} `json:"start"`
		End struct {
			Line int `json:"line"`
		} `json:"end"`
	} `json:"statementMap"`
	S map[string]int `json:"s"`
}

func parseIstanbul(path, absRoot string, layout covtypes.Layout) (map[string][]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read istanbul output: %w", err)
	}
	var doc map[string]istanbulFile
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse istanbul output: %w", err)
	}
	out := map[string][]int{}
	for absFile, cov := range doc {
		rel, err := filepath.Rel(absRoot, absFile)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		rel = filepath.ToSlash(rel)
		if layout.ClassifyFile(rel) != covtypes.KindSource {
			continue
		}
		lines := map[int]bool{}
		for id, hits := range cov.S {
			if hits == 0 {
				continue
			}
			stmt, ok := cov.StatementMap[id]
			if !ok {
				continue
			}
			for l := stmt.Start.Line; l <= stmt.End.Line; l++ {
				lines[l] = true
			}
		}
		if len(lines) == 0 {
			continue
		}
		sorted := make([]int, 0, len(lines))
		for l := range lines {
			sorted = append(sorted, l)
		}
		sort.Ints(sorted)
		out[rel] = sorted
	}
	return out, nil
}
