// Package gotest implements the RunnerAdapter for the Go / `go test`
// ecosystem — the second supported runner, built first to dogfood
// SpecRoster on its own test suite.
//
// Go differs from Python on every axis the adapter seam exists for:
//   - tests live NEXT TO source (_test.go), not in a tests/ directory
//   - identity is package-based: canonical ID = <import-path>.<TestName>
//     (matches gotestsum's JUnit classname natively, no module stripping)
//   - coverage has no per-test contexts; the cmd/specroster-gocover
//     collector produces per-test coverage by running each test with its
//     own -coverprofile (with -coverpkg=./... for cross-package coverage)
//
// Collection inputs:
//   - per-test coverage: the collector's JSON (format specroster/go-cover/v1)
//   - test inventory: the collector's collected list, one "pkg::TestName"
//     native ID per line
package gotest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/SpecRoster/Collector/covtypes"
)

// CoverageFormat is the format tag the collector writes and ParseCoverage
// requires.
const CoverageFormat = "specroster/go-cover/v1"

// Adapter is the Go runner adapter.
type Adapter struct{}

// Name implements runner.Adapter.
func (Adapter) Name() string { return "gotest" }

// CoverJSON is the collector's output document (exported: the collector in
// cmd/specroster-gocover writes it).
type CoverJSON struct {
	Format string `json:"format"`
	Module string `json:"module"`
	// Tests: canonical test ID → repo-relative file → covered lines.
	Tests map[string]map[string][]int `json:"tests"`
}

// ParseCoverage implements runner.Adapter.
func (Adapter) ParseCoverage(r io.Reader) (*covtypes.Coverage, error) {
	var doc CoverJSON
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("gotest: parse coverage json: %w", err)
	}
	if doc.Format != CoverageFormat {
		return nil, fmt.Errorf("gotest: unexpected coverage format %q (need %s — produced by specroster-gocover)", doc.Format, CoverageFormat)
	}
	cov := &covtypes.Coverage{LineTests: map[string]map[int][]string{}}
	for test, files := range doc.Tests {
		for file, lines := range files {
			file = strings.ReplaceAll(file, "\\", "/")
			byLine := cov.LineTests[file]
			if byLine == nil {
				byLine = map[int][]string{}
				cov.LineTests[file] = byLine
			}
			for _, line := range lines {
				byLine[line] = append(byLine[line], test)
			}
		}
	}
	return cov, nil
}

// ParseTestList implements runner.Adapter: one "pkg::TestName" per line.
func (Adapter) ParseTestList(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		native := strings.TrimSpace(sc.Text())
		if !strings.Contains(native, "::") {
			continue
		}
		key := (Adapter{}).NormalizeNative(native)
		if key == "" {
			continue
		}
		if _, ok := out[key]; !ok {
			out[key] = native
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("gotest: read test list: %w", err)
	}
	return out, nil
}

// InvocationArgs implements runner.Adapter: native IDs ("pkg::TestName")
// become `-run '^(A|B)$' pkg1 pkg2`. The -run regex applies to every listed
// package, so a name shared across packages over-selects — conservative in
// the safe direction.
func (Adapter) InvocationArgs(nativeIDs []string) []string {
	pkgs := map[string]bool{}
	names := map[string]bool{}
	for _, id := range nativeIDs {
		pkg, name, ok := strings.Cut(id, "::")
		if !ok || pkg == "" || name == "" {
			continue
		}
		pkgs[pkg] = true
		names[regexp.QuoteMeta(strings.SplitN(name, "/", 2)[0])] = true
	}
	if len(names) == 0 {
		return nil
	}
	args := []string{"-run", "^(" + strings.Join(sortedKeys(names), "|") + ")$"}
	return append(args, sortedKeys(pkgs)...)
}

// NormalizeJUnit implements runner.Adapter: gotestsum's JUnit output uses
// classname = full import path, name = TestName (subtests as TestName/sub).
// Subtests collapse onto their parent — one identity per Test function.
func (Adapter) NormalizeJUnit(classname, name string) string {
	name = strings.SplitN(name, "/", 2)[0]
	if classname == "" || name == "" {
		return ""
	}
	return classname + "." + name
}

// NormalizeNative implements runner.Adapter for "pkg::TestName" IDs.
func (Adapter) NormalizeNative(nativeID string) string {
	pkg, name, ok := strings.Cut(nativeID, "::")
	if !ok {
		return ""
	}
	return (Adapter{}).NormalizeJUnit(pkg, name)
}

// Layout implements runner.Adapter. testDir is meaningless in Go (tests
// live next to source); srcDir optionally scopes a subdirectory (monorepo).
func (Adapter) Layout(srcDir, _ string) covtypes.Layout {
	return goLayout{srcDir: srcDir}
}

type goLayout struct{ srcDir string }

func (l goLayout) ClassifyFile(p string) covtypes.FileKind {
	p = strings.ReplaceAll(p, "\\", "/")
	if !strings.HasSuffix(p, ".go") {
		return covtypes.KindOther
	}
	if l.srcDir != "" && l.srcDir != "." && !underDir(p, l.srcDir) {
		return covtypes.KindOther
	}
	if strings.HasSuffix(p, "_test.go") {
		return covtypes.KindTest
	}
	return covtypes.KindSource
}

// TestsInTestFile maps a changed _test.go file to every test in its package
// (per-test defining-file is not derivable from coverage; package
// granularity is the conservative correct answer).
func (l goLayout) TestsInTestFile(path string, all covtypes.TestSet) covtypes.TestSet {
	dir := pkgDirOf(path)
	out := covtypes.TestSet{}
	for t := range all {
		if pkgMatchesDir(pkgOf(t), dir) {
			out[t] = struct{}{}
		}
	}
	return out
}

// pkgOf splits "import/path.TestName" at the final dot (test function names
// cannot contain dots; import paths can).
func pkgOf(canonical string) string {
	i := strings.LastIndex(canonical, ".")
	if i < 0 {
		return canonical
	}
	return canonical[:i]
}

func pkgDirOf(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return "."
	}
	return path[:i]
}

// pkgMatchesDir reports whether an import path corresponds to a
// repo-relative package directory (suffix match: the import path carries
// the module prefix the repo path lacks). Root-package tests ("." dir)
// conservatively match everything.
func pkgMatchesDir(pkg, dir string) bool {
	if dir == "." {
		return true
	}
	return pkg == dir || strings.HasSuffix(pkg, "/"+dir)
}

// EntriesForNewTestFiles implements runner.Adapter: Go cannot run a single
// test file; new test files map to their package directory (deduped), which
// the Action runs whole.
func (Adapter) EntriesForNewTestFiles(files []string) []string {
	dirs := map[string]bool{}
	for _, f := range files {
		dirs["./"+pkgDirOf(f)] = true
	}
	return sortedKeys(dirs)
}

// FileEntryCovers implements runner.Adapter: a "./dir" package entry covers
// every test in that package.
func (Adapter) FileEntryCovers(entry, canonical string) bool {
	dir, ok := strings.CutPrefix(entry, "./")
	if !ok {
		return false
	}
	return pkgMatchesDir(pkgOf(canonical), dir)
}

func underDir(p, dir string) bool {
	dir = strings.TrimSuffix(dir, "/")
	return p == dir || strings.HasPrefix(p, dir+"/")
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
