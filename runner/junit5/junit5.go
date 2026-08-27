// Package junit5 implements the RunnerAdapter for the JVM / JUnit 5
// ecosystem (Maven Surefire first; Gradle shares the JUnit XML format) —
// targeted at Java monolith codebases where full suites run for hours.
//
// Ecosystem facts the adapter absorbs:
//   - identity: canonical ID = package.Class.method; nested classes keep
//     their "$Nested" binary-name form in the class part; parametrized
//     invocations ("method(int)[1]") and Surefire's "method()" rendering
//     collapse onto their method
//   - native ID = "package.Class::method"; invocation renders a Surefire
//     -Dtest= filter ("Class#m1+m2", classes comma-joined)
//   - tests conventionally live under src/test/java and/or in *Test.java,
//     *Tests.java, *IT.java files; classification is heuristic on both
//   - coverage has no per-test contexts; cmd/specroster-jvmcover runs
//     each test with its own JaCoCo agent collection
package junit5

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
const CoverageFormat = "specroster/jvm-cover/v1"

// classEntryPrefix marks a manifest entry meaning "run every test whose
// class short-name matches" (used for brand-new test files).
const classEntryPrefix = "class:"

// Adapter is the JVM / JUnit 5 runner adapter.
type Adapter struct{}

// Name implements runner.Adapter.
func (Adapter) Name() string { return "junit" }

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
		return nil, fmt.Errorf("junit: parse coverage json: %w", err)
	}
	if doc.Format != CoverageFormat {
		return nil, fmt.Errorf("junit: unexpected coverage format %q (need %s — produced by specroster-jvmcover)", doc.Format, CoverageFormat)
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

// ParseTestList implements runner.Adapter: one "package.Class::method"
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
		return nil, fmt.Errorf("junit: read test list: %w", err)
	}
	return out, nil
}

// InvocationArgs implements runner.Adapter: a single Surefire -Dtest=
// filter. Methods of the same class are grouped with "+"
// ("Class#m1+m2"), classes are comma-joined; ordering is deterministic.
func (Adapter) InvocationArgs(nativeIDs []string) []string {
	byClass := map[string]map[string]bool{}
	for _, id := range nativeIDs {
		class, method, ok := strings.Cut(id, "::")
		if !ok || class == "" {
			continue
		}
		method = stripMethodSuffix(method)
		if method == "" {
			continue
		}
		if byClass[class] == nil {
			byClass[class] = map[string]bool{}
		}
		byClass[class][method] = true
	}
	if len(byClass) == 0 {
		return nil
	}
	classes := make([]string, 0, len(byClass))
	for c := range byClass {
		classes = append(classes, c)
	}
	sort.Strings(classes)
	entries := make([]string, 0, len(classes))
	for _, c := range classes {
		entries = append(entries, c+"#"+strings.Join(sortedKeys(byClass[c]), "+"))
	}
	return []string{"-Dtest=" + strings.Join(entries, ",")}
}

// NormalizeJUnit implements runner.Adapter. Surefire's JUnit XML emits
// classname = fully-qualified class (nested classes as "Outer$Nested"),
// name = method, possibly rendered "method()" or with a parametrized
// invocation suffix "method(int)[1]" — both collapse onto the method.
func (Adapter) NormalizeJUnit(classname, name string) string {
	classname = strings.TrimSpace(classname)
	name = stripMethodSuffix(name)
	if classname == "" || name == "" {
		return ""
	}
	return classname + "." + name
}

// NormalizeNative implements runner.Adapter for "package.Class::method"
// IDs.
func (Adapter) NormalizeNative(nativeID string) string {
	class, method, ok := strings.Cut(nativeID, "::")
	if !ok {
		return ""
	}
	return (Adapter{}).NormalizeJUnit(class, method)
}

// stripMethodSuffix drops Surefire's "()" rendering and parametrized
// "(args)[n]" invocation suffixes from a JUnit method name.
func stripMethodSuffix(s string) string {
	if i := strings.IndexAny(s, "(["); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// Layout implements runner.Adapter. testDir is unused (tests live under
// src/test/java by Maven convention); srcDir optionally scopes a monorepo.
func (Adapter) Layout(srcDir, _ string) covtypes.Layout {
	return javaLayout{srcDir: srcDir}
}

type javaLayout struct{ srcDir string }

func (l javaLayout) ClassifyFile(p string) covtypes.FileKind {
	p = strings.ReplaceAll(p, "\\", "/")
	if !strings.HasSuffix(p, ".java") {
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

// isTestPath: under a Maven "src/test/" tree, or a file named *Test.java /
// *Tests.java / *IT.java — the dominant JVM conventions.
func isTestPath(p string) bool {
	if strings.HasPrefix(p, "src/test/") || strings.Contains(p, "/src/test/") {
		return true
	}
	base := p[strings.LastIndex(p, "/")+1:]
	return strings.HasSuffix(base, "Test.java") ||
		strings.HasSuffix(base, "Tests.java") ||
		strings.HasSuffix(base, "IT.java")
}

// TestsInTestFile maps a changed test file to the tests of the class it
// conventionally defines (FooTest.java → class FooTest, including
// @Nested inner classes FooTest$Nested).
func (l javaLayout) TestsInTestFile(path string, all covtypes.TestSet) covtypes.TestSet {
	path = strings.ReplaceAll(path, "\\", "/")
	base := strings.TrimSuffix(path[strings.LastIndex(path, "/")+1:], ".java")
	out := covtypes.TestSet{}
	if base == "" {
		return out
	}
	for t := range all {
		if classMatches(classShortName(t), base) {
			out[t] = struct{}{}
		}
	}
	return out
}

// classShortName("demo.CalcTest$Nested.adds") → "CalcTest$Nested" (the
// last dot-segment of the class part; "$" is not a package separator).
func classShortName(canonical string) string {
	parts := strings.Split(canonical, ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2]
}

// classMatches reports whether a class short-name belongs to the
// top-level class `base` (itself or one of its $Nested classes).
func classMatches(short, base string) bool {
	return short == base || strings.HasPrefix(short, base+"$")
}

// EntriesForNewTestFiles implements runner.Adapter: a new test file maps
// to a class-name entry the Action turns into a -Dtest= filter.
func (Adapter) EntriesForNewTestFiles(files []string) []string {
	entries := map[string]bool{}
	for _, f := range files {
		f = strings.ReplaceAll(f, "\\", "/")
		base := strings.TrimSuffix(f[strings.LastIndex(f, "/")+1:], ".java")
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
	return classMatches(classShortName(canonical), base)
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
