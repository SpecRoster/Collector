// Package dotnet implements the RunnerAdapter for the .NET / `dotnet test`
// ecosystem (xUnit first; NUnit/MSTest share the same VSTest filter syntax).
//
// Ecosystem facts the adapter absorbs:
//   - identity: canonical ID = Namespace.Class.Method; theory/parametrized
//     cases ("Method(x: 1)") collapse onto their method
//   - native ID = "Namespace.Class::Method"; invocation renders VSTest
//     --filter expressions (FullyQualifiedName~, contains-match — slight
//     over-selection is the safe direction)
//   - tests conventionally live in *Tests projects/files next to or beside
//     the product code; classification is heuristic on path segments
//   - coverage has no per-test contexts; cmd/specroster-dotnetcover runs
//     each test with its own coverlet.msbuild collection
package dotnet

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
const CoverageFormat = "specroster/dotnet-cover/v1"

// classEntryPrefix marks a manifest entry meaning "run every test whose
// class short-name matches" (used for brand-new test files).
const classEntryPrefix = "class:"

// Adapter is the .NET runner adapter.
type Adapter struct{}

// Name implements runner.Adapter.
func (Adapter) Name() string { return "dotnet" }

// CoverJSON is the collector's output (same shape as the Go collector's).
type CoverJSON struct {
	Format string `json:"format"`
	// Tests: canonical test ID → repo-relative file → covered lines.
	Tests map[string]map[string][]int `json:"tests"`
}

// ParseCoverage implements runner.Adapter.
func (Adapter) ParseCoverage(r io.Reader) (*covtypes.Coverage, error) {
	var doc CoverJSON
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("dotnet: parse coverage json: %w", err)
	}
	if doc.Format != CoverageFormat {
		return nil, fmt.Errorf("dotnet: unexpected coverage format %q (need %s — produced by specroster-dotnetcover)", doc.Format, CoverageFormat)
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

// ParseTestList implements runner.Adapter: one "Namespace.Class::Method"
// per line.
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
		return nil, fmt.Errorf("dotnet: read test list: %w", err)
	}
	return out, nil
}

// InvocationArgs implements runner.Adapter: a single VSTest --filter
// expression ORing FullyQualifiedName~ contains-matches.
func (Adapter) InvocationArgs(nativeIDs []string) []string {
	terms := map[string]bool{}
	for _, id := range nativeIDs {
		if fqn := (Adapter{}).NormalizeNative(id); fqn != "" {
			terms["FullyQualifiedName~"+fqn] = true
		}
	}
	if len(terms) == 0 {
		return nil
	}
	return []string{"--filter", strings.Join(sortedKeys(terms), "|")}
}

// NormalizeJUnit implements runner.Adapter. JUnitXml.TestLogger emits
// classname = Namespace.Class, name = Method (theories carry "(args)").
func (Adapter) NormalizeJUnit(classname, name string) string {
	name = strings.TrimSpace(stripArgs(name))
	// Some loggers emit name as the full FQN; avoid double-prefixing.
	if classname != "" && strings.HasPrefix(name, classname+".") {
		return name
	}
	if classname == "" || name == "" {
		return ""
	}
	return classname + "." + name
}

// NormalizeNative implements runner.Adapter for "ns.Class::Method" IDs.
func (Adapter) NormalizeNative(nativeID string) string {
	class, method, ok := strings.Cut(nativeID, "::")
	if !ok || class == "" || method == "" {
		return ""
	}
	return class + "." + stripArgs(method)
}

// stripArgs drops a trailing "(...)" theory-argument suffix.
func stripArgs(s string) string {
	if i := strings.Index(s, "("); i >= 0 {
		return s[:i]
	}
	return s
}

// Layout implements runner.Adapter. testDir is unused (tests live in
// *Tests projects by convention); srcDir optionally scopes a monorepo.
func (Adapter) Layout(srcDir, _ string) covtypes.Layout {
	return csLayout{srcDir: srcDir}
}

type csLayout struct{ srcDir string }

func (l csLayout) ClassifyFile(p string) covtypes.FileKind {
	p = strings.ReplaceAll(p, "\\", "/")
	if !strings.HasSuffix(p, ".cs") {
		return covtypes.KindOther
	}
	if l.srcDir != "" && l.srcDir != "." && !underDir(p, l.srcDir) {
		return covtypes.KindOther
	}
	if isTestPath(p) {
		return covtypes.KindTest
	}
	return covtypes.KindSource
}

// isTestPath: under a "*Tests"/"*.Tests"/"*Test" directory, or a file named
// *Tests.cs / *Test.cs — the dominant .NET conventions.
func isTestPath(p string) bool {
	segs := strings.Split(p, "/")
	base := segs[len(segs)-1]
	if strings.HasSuffix(base, "Tests.cs") || strings.HasSuffix(base, "Test.cs") {
		return true
	}
	for _, d := range segs[:len(segs)-1] {
		if strings.HasSuffix(d, "Tests") || strings.HasSuffix(d, ".Tests") || strings.HasSuffix(d, "Test") {
			return true
		}
	}
	return false
}

// TestsInTestFile maps a changed test file to the tests of the class it
// conventionally defines (FooTests.cs → class FooTests).
func (l csLayout) TestsInTestFile(path string, all covtypes.TestSet) covtypes.TestSet {
	path = strings.ReplaceAll(path, "\\", "/")
	base := strings.TrimSuffix(path[strings.LastIndex(path, "/")+1:], ".cs")
	out := covtypes.TestSet{}
	for t := range all {
		if classShortName(t) == base {
			out[t] = struct{}{}
		}
	}
	return out
}

// classShortName("Ns.Sub.CalcTests.AddWorks") → "CalcTests".
func classShortName(canonical string) string {
	parts := strings.Split(canonical, ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2]
}

// EntriesForNewTestFiles implements runner.Adapter: a new test file maps to
// a class-name entry the Action turns into a FullyQualifiedName~ filter.
func (Adapter) EntriesForNewTestFiles(files []string) []string {
	entries := map[string]bool{}
	for _, f := range files {
		f = strings.ReplaceAll(f, "\\", "/")
		base := strings.TrimSuffix(f[strings.LastIndex(f, "/")+1:], ".cs")
		if base != "" {
			entries[classEntryPrefix+base] = true
		}
	}
	return sortedKeys(entries)
}

// FileEntryCovers implements runner.Adapter.
func (Adapter) FileEntryCovers(entry, canonical string) bool {
	base, ok := strings.CutPrefix(entry, classEntryPrefix)
	if !ok {
		return false
	}
	return classShortName(canonical) == base
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
