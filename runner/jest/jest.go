// Package jest implements the RunnerAdapter for the JavaScript/TypeScript /
// Jest ecosystem — the fourth supported runner (largest ecosystem by
// developer share).
//
// Identity is deliberately TEST-FILE granular: the canonical test ID is the
// spec file's repo-relative path. JS test names are free strings inside
// describe blocks (no stable FQN), Jest schedules and parallelizes per
// file, and per-file selection preserves fixture/setup semantics — the same
// granularity argument the design review made for selection generally.
// Rollups therefore track spec files, not individual `it()` cases.
//
// Collection inputs come from cmd/specroster-jestcover (one
// `jest --coverage` run per spec file — Istanbul has no per-test contexts).
// JUnit ingestion requires the customer to configure jest-junit with
// classNameTemplate "{filepath}" so results reconcile to the same identity.
package jest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/SpecRoster/Collector/covtypes"
)

// CoverageFormat tags the collector's output.
const CoverageFormat = "specroster/jest-cover/v1"

// Adapter is the Jest runner adapter.
type Adapter struct{}

// Name implements runner.Adapter.
func (Adapter) Name() string { return "jest" }

// CoverJSON is the collector's output.
type CoverJSON struct {
	Format string `json:"format"`
	// Tests: spec file (repo-relative) → source file → covered lines.
	Tests map[string]map[string][]int `json:"tests"`
}

// ParseCoverage implements runner.Adapter.
func (Adapter) ParseCoverage(r io.Reader) (*covtypes.Coverage, error) {
	var doc CoverJSON
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("jest: parse coverage json: %w", err)
	}
	if doc.Format != CoverageFormat {
		return nil, fmt.Errorf("jest: unexpected coverage format %q (need %s — produced by specroster-jestcover)", doc.Format, CoverageFormat)
	}
	cov := &covtypes.Coverage{LineTests: map[string]map[int][]string{}}
	for spec, files := range doc.Tests {
		spec = normalizePath(spec)
		for file, lines := range files {
			file = normalizePath(file)
			byLine := cov.LineTests[file]
			if byLine == nil {
				byLine = map[int][]string{}
				cov.LineTests[file] = byLine
			}
			for _, line := range lines {
				byLine[line] = append(byLine[line], spec)
			}
		}
	}
	return cov, nil
}

// ParseTestList implements runner.Adapter: one spec file path per line;
// canonical and native IDs are the same path.
func (Adapter) ParseTestList(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		p := normalizePath(strings.TrimSpace(sc.Text()))
		if p == "" {
			continue
		}
		out[p] = p
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("jest: read test list: %w", err)
	}
	return out, nil
}

// InvocationArgs implements runner.Adapter: jest accepts spec paths as
// positional arguments (matched with --runTestsByPath in the Action).
func (Adapter) InvocationArgs(nativeIDs []string) []string {
	out := make([]string, 0, len(nativeIDs))
	for _, id := range nativeIDs {
		if p := normalizePath(id); p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// NormalizeJUnit implements runner.Adapter. With jest-junit configured as
// classNameTemplate "{filepath}", classname is the spec path; every case in
// a file collapses onto the file identity.
func (Adapter) NormalizeJUnit(classname, _ string) string {
	return normalizePath(classname)
}

// NormalizeNative implements runner.Adapter.
func (Adapter) NormalizeNative(nativeID string) string {
	return normalizePath(nativeID)
}

func normalizePath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	return strings.TrimPrefix(p, "./")
}

// Layout implements runner.Adapter. testDir is unused (spec files are
// recognized by convention wherever they live); srcDir optionally scopes a
// monorepo.
func (Adapter) Layout(srcDir, _ string) covtypes.Layout {
	return jsLayout{srcDir: srcDir}
}

var sourceExts = []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs"}

type jsLayout struct{ srcDir string }

func (l jsLayout) ClassifyFile(p string) covtypes.FileKind {
	p = normalizePath(p)
	ext := ""
	for _, e := range sourceExts {
		if strings.HasSuffix(p, e) {
			ext = e
			break
		}
	}
	if ext == "" {
		return covtypes.KindOther
	}
	if l.srcDir != "" && l.srcDir != "." && !underDir(p, l.srcDir) {
		return covtypes.KindOther
	}
	if isSpecPath(p, ext) {
		return covtypes.KindTest
	}
	return covtypes.KindSource
}

// isSpecPath: *.test.* / *.spec.* basenames, or anything under a
// __tests__/ directory — Jest's default testMatch conventions.
func isSpecPath(p, ext string) bool {
	base := p[strings.LastIndex(p, "/")+1:]
	stem := strings.TrimSuffix(base, ext)
	if strings.HasSuffix(stem, ".test") || strings.HasSuffix(stem, ".spec") {
		return true
	}
	return strings.Contains(p, "__tests__/")
}

// TestsInTestFile: identity IS the file — a changed spec file selects
// itself (when the index knows it).
func (l jsLayout) TestsInTestFile(path string, all covtypes.TestSet) covtypes.TestSet {
	path = normalizePath(path)
	out := covtypes.TestSet{}
	if _, ok := all[path]; ok {
		out[path] = struct{}{}
	}
	return out
}

// EntriesForNewTestFiles implements runner.Adapter: new spec files run
// directly by path.
func (Adapter) EntriesForNewTestFiles(files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, normalizePath(f))
	}
	sort.Strings(out)
	return out
}

// FileEntryCovers implements runner.Adapter: a path entry covers exactly
// the spec file it names.
func (Adapter) FileEntryCovers(entry, canonical string) bool {
	return normalizePath(entry) == normalizePath(canonical)
}

func underDir(p, dir string) bool {
	dir = strings.TrimSuffix(dir, "/")
	return p == dir || strings.HasPrefix(p, dir+"/")
}
