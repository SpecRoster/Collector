// Package cargo implements the RunnerAdapter for the Rust / `cargo test`
// ecosystem (libtest), with cargo-nextest as the JUnit producer and
// cmd/specroster-rustcover (cargo-llvm-cov) as the coverage collector.
//
// Identity: canonical = native = the libtest test path exactly as
// `cargo test -- --list` prints it — "module::path::test_name". The path
// naturally contains "::", which is also the manifest convention's marker
// for native test IDs, so NormalizeNative is normalization-light: trim
// whitespace, and reject anything without "::" (those are file entries).
//
// Layout: unit tests live INSIDE source files (#[cfg(test)] mod tests), so
// .rs files under src are KindSource — a changed source file selects the
// tests covering it, which includes its own unit tests. Integration tests
// (tests/) and benches (benches/) are KindTest. Each integration test file
// tests/foo.rs compiles to its own test binary whose root module is the
// file stem, so SpecRoster's convention is that integration test paths are
// rooted at the file stem: "foo::test_name" (cargo-nextest's classname, or
// an explicit `mod foo { ... }` wrapper in the file, supplies the root).
//
// Collection inputs:
//   - per-test coverage: the collector's JSON (specroster/rust-cover/v1)
//   - test inventory: the collector's collected list, one libtest path
//     ("module::path::test_name") per line
package cargo

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/SpecRoster/Collector/covtypes"
)

// CoverageFormat is the format tag the collector writes and ParseCoverage
// requires.
const CoverageFormat = "specroster/rust-cover/v1"

// Adapter is the Rust runner adapter.
type Adapter struct{}

// Name implements runner.Adapter.
func (Adapter) Name() string { return "cargo" }

// CoverJSON is the collector's output document (exported: the collector in
// cmd/specroster-rustcover writes it). Shape-identical to gotest's.
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
		return nil, fmt.Errorf("cargo: parse coverage json: %w", err)
	}
	if doc.Format != CoverageFormat {
		return nil, fmt.Errorf("cargo: unexpected coverage format %q (need %s — produced by specroster-rustcover)", doc.Format, CoverageFormat)
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

// ParseTestList implements runner.Adapter: one libtest path
// ("module::path::test_name") per line. Canonical and native are the same
// string.
func (Adapter) ParseTestList(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		native := strings.TrimSpace(sc.Text())
		key := (Adapter{}).NormalizeNative(native)
		if key == "" {
			continue
		}
		if _, ok := out[key]; !ok {
			out[key] = key
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("cargo: read test list: %w", err)
	}
	return out, nil
}

// InvocationArgs implements runner.Adapter: libtest runs exactly the named
// tests via `-- --exact name1 name2 ...` (every positional filter after
// --exact must match a full test path).
func (Adapter) InvocationArgs(nativeIDs []string) []string {
	names := map[string]bool{}
	for _, id := range nativeIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		names[id] = true
	}
	if len(names) == 0 {
		return nil
	}
	return append([]string{"--", "--exact"}, sortedKeys(names)...)
}

// NormalizeJUnit implements runner.Adapter: cargo-nextest's JUnit output
// puts the full test path (contains "::") in name and the binary/crate in
// classname. When name is already a full path, it is the canonical ID;
// otherwise the classname supplies the root module.
func (Adapter) NormalizeJUnit(classname, name string) string {
	classname = strings.TrimSpace(classname)
	name = strings.TrimSpace(name)
	if strings.Contains(name, "::") {
		return name
	}
	if classname != "" && name != "" {
		return classname + "::" + name
	}
	return ""
}

// NormalizeNative implements runner.Adapter: native IDs are already
// canonical libtest paths. Strings without "::" are not test IDs (the
// manifest convention treats them as file entries) and normalize to "".
func (Adapter) NormalizeNative(nativeID string) string {
	nativeID = strings.TrimSpace(nativeID)
	if !strings.Contains(nativeID, "::") {
		return ""
	}
	return nativeID
}

// Layout implements runner.Adapter. testDir is meaningless in Rust (the
// tests/ and benches/ conventions are fixed by cargo); srcDir optionally
// scopes a subdirectory (monorepo).
func (Adapter) Layout(srcDir, _ string) covtypes.Layout {
	return cargoLayout{srcDir: srcDir}
}

type cargoLayout struct{ srcDir string }

func (l cargoLayout) ClassifyFile(p string) covtypes.FileKind {
	p = strings.ReplaceAll(p, "\\", "/")
	if !strings.HasSuffix(p, ".rs") {
		return covtypes.KindOther
	}
	if l.srcDir != "" && l.srcDir != "." && !underDir(p, l.srcDir) {
		return covtypes.KindOther
	}
	if isCargoTestPath(p) {
		return covtypes.KindTest
	}
	return covtypes.KindSource
}

// isCargoTestPath reports whether the path sits under a cargo "tests/"
// (integration tests) or "benches/" directory. The marker segment must not
// itself be nested under a "src" segment: src/tests/mod.rs is a unit-test
// module compiled into the lib, i.e. source. This recognizes the
// crate-top-level tests/ dir both at the repo root and inside workspace
// members (crates/foo/tests/bar.rs).
func isCargoTestPath(p string) bool {
	segs := strings.Split(p, "/")
	for _, s := range segs[:len(segs)-1] { // exclude the file name itself
		switch s {
		case "src":
			return false
		case "tests", "benches":
			return true
		}
	}
	return false
}

// TestsInTestFile maps a changed integration test file to its tests.
// Convention: each integration test file tests/foo.rs is its own test
// binary whose root module is the file stem, so its tests are exactly the
// canonical IDs whose FIRST "::" segment equals "foo".
func (l cargoLayout) TestsInTestFile(path string, all covtypes.TestSet) covtypes.TestSet {
	stem := fileStem(path)
	out := covtypes.TestSet{}
	for t := range all {
		if firstSegment(t) == stem {
			out[t] = struct{}{}
		}
	}
	return out
}

// EntriesForNewTestFiles implements runner.Adapter: a new tests/foo.rs is a
// new test binary; the manifest entry "bin:foo" tells the Action to run
// that whole binary (cargo test --test foo / nextest -E 'binary(foo)').
func (Adapter) EntriesForNewTestFiles(files []string) []string {
	bins := map[string]bool{}
	for _, f := range files {
		if stem := fileStem(f); stem != "" {
			bins["bin:"+stem] = true
		}
	}
	return sortedKeys(bins)
}

// FileEntryCovers implements runner.Adapter: a "bin:foo" entry covers every
// test whose first "::" segment is "foo" (the binary's root module).
func (Adapter) FileEntryCovers(entry, canonical string) bool {
	stem, ok := strings.CutPrefix(entry, "bin:")
	if !ok || stem == "" {
		return false
	}
	return firstSegment(canonical) == stem
}

// firstSegment returns the canonical ID's root module (text before the
// first "::"), or the whole string when there is no "::".
func firstSegment(canonical string) string {
	seg, _, _ := strings.Cut(canonical, "::")
	return seg
}

// fileStem returns the file's base name without the .rs extension.
func fileStem(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	base := path[strings.LastIndex(path, "/")+1:]
	return strings.TrimSuffix(base, ".rs")
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
