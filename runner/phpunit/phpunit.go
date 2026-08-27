// Package phpunit implements the RunnerAdapter for the PHP / PHPUnit
// ecosystem — composer-managed projects running `vendor/bin/phpunit`.
//
// Ecosystem facts the adapter absorbs:
//   - identity: canonical ID = FQCN with backslashes converted to dots,
//     plus "." and the method ("App\Tests\CalcTest" + "testAdd" →
//     "App.Tests.CalcTest.testAdd"); data-provider cases ("testAdd with
//     data set #0", "testAdd#0") collapse onto their method
//   - native ID = "App\Tests\CalcTest::testAdd" (PHP's own syntax);
//     invocation renders an anchored --filter regex with the namespace
//     backslashes regex-escaped
//   - tests conventionally live under a tests/ directory and/or in
//     *Test.php files; classification is heuristic on path segments
//   - coverage has no per-test contexts; cmd/specroster-phpcover runs
//     each test with its own --coverage-clover collection under
//     XDEBUG_MODE=coverage
package phpunit

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
const CoverageFormat = "specroster/php-cover/v1"

// classEntryPrefix marks a manifest entry meaning "run every test whose
// class short-name matches" (used for brand-new test files).
const classEntryPrefix = "class:"

// Adapter is the PHPUnit runner adapter.
type Adapter struct{}

// Name implements runner.Adapter.
func (Adapter) Name() string { return "phpunit" }

// CoverJSON is the collector's output (same shape as the dotnet collector's).
type CoverJSON struct {
	Format string `json:"format"`
	// Tests: canonical test ID → repo-relative file → covered lines.
	Tests map[string]map[string][]int `json:"tests"`
}

// ParseCoverage implements runner.Adapter.
func (Adapter) ParseCoverage(r io.Reader) (*covtypes.Coverage, error) {
	var doc CoverJSON
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("phpunit: parse coverage json: %w", err)
	}
	if doc.Format != CoverageFormat {
		return nil, fmt.Errorf("phpunit: unexpected coverage format %q (need %s — produced by specroster-phpcover)", doc.Format, CoverageFormat)
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

// ParseTestList implements runner.Adapter: one "Ns\Class::method" per line.
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
		return nil, fmt.Errorf("phpunit: read test list: %w", err)
	}
	return out, nil
}

// InvocationArgs implements runner.Adapter: a single anchored --filter
// regex ORing exact "Ns\Class::method" matches. PHPUnit matches the
// pattern against the PHP-native name, so namespace backslashes must be
// regex-escaped (each "\" becomes "\\" in the final argument string).
func (Adapter) InvocationArgs(nativeIDs []string) []string {
	terms := map[string]bool{}
	for _, id := range nativeIDs {
		class, method, ok := strings.Cut(id, "::")
		if !ok || class == "" {
			continue
		}
		method = stripDataSet(method)
		if method == "" {
			continue
		}
		terms[strings.ReplaceAll(class, `\`, `\\`)+"::"+method] = true
	}
	if len(terms) == 0 {
		return nil
	}
	return []string{"--filter", "^(?:" + strings.Join(sortedKeys(terms), "|") + ")$"}
}

// NormalizeJUnit implements runner.Adapter. PHPUnit's JUnit XML carries
// classname = FQCN (backslash or dot separated, depending on attribute)
// and name = method (data-provider cases carry a " with data set" suffix).
func (Adapter) NormalizeJUnit(classname, name string) string {
	name = stripDataSet(strings.TrimSpace(name))
	// Some emitters put the native "Ns\Class::method" in name already.
	if strings.Contains(name, "::") {
		return (Adapter{}).NormalizeNative(name)
	}
	if classname == "" || name == "" {
		return ""
	}
	return strings.ReplaceAll(classname, `\`, ".") + "." + name
}

// NormalizeNative implements runner.Adapter for "Ns\Class::method" IDs.
func (Adapter) NormalizeNative(nativeID string) string {
	class, method, ok := strings.Cut(nativeID, "::")
	if !ok || class == "" {
		return ""
	}
	method = stripDataSet(method)
	if method == "" {
		return ""
	}
	return strings.ReplaceAll(class, `\`, ".") + "." + method
}

// stripDataSet drops a PHPUnit data-provider suffix from a method name:
// `testAdd with data set #0` / `testAdd with data set "two"` → "testAdd",
// plus the compact "testAdd#0" form.
func stripDataSet(s string) string {
	if i := strings.Index(s, " with data set"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "#"); i >= 0 && isDigits(s[i+1:]) {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Layout implements runner.Adapter. testDir is unused (tests live under a
// tests/ directory or in *Test.php files by convention); srcDir optionally
// scopes which product files count as source.
func (Adapter) Layout(srcDir, _ string) covtypes.Layout {
	return phpLayout{srcDir: srcDir}
}

type phpLayout struct{ srcDir string }

func (l phpLayout) ClassifyFile(p string) covtypes.FileKind {
	p = strings.ReplaceAll(p, "\\", "/")
	if !strings.HasSuffix(p, ".php") {
		return covtypes.KindOther
	}
	if isTestPath(p) {
		return covtypes.KindTest
	}
	if l.srcDir != "" && l.srcDir != "." && !underDir(p, l.srcDir) {
		return covtypes.KindOther
	}
	return covtypes.KindSource
}

// isTestPath: under a "tests" directory segment, or a file named *Test.php
// — the dominant PHPUnit conventions.
func isTestPath(p string) bool {
	segs := strings.Split(p, "/")
	if strings.HasSuffix(segs[len(segs)-1], "Test.php") {
		return true
	}
	for _, d := range segs[:len(segs)-1] {
		if d == "tests" {
			return true
		}
	}
	return false
}

// TestsInTestFile maps a changed test file to the tests of the class it
// conventionally defines (CalcTest.php → class CalcTest).
func (l phpLayout) TestsInTestFile(path string, all covtypes.TestSet) covtypes.TestSet {
	path = strings.ReplaceAll(path, "\\", "/")
	base := strings.TrimSuffix(path[strings.LastIndex(path, "/")+1:], ".php")
	out := covtypes.TestSet{}
	for t := range all {
		if classShortName(t) == base {
			out[t] = struct{}{}
		}
	}
	return out
}

// classShortName("App.Tests.CalcTest.testAdd") → "CalcTest".
func classShortName(canonical string) string {
	parts := strings.Split(canonical, ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2]
}

// EntriesForNewTestFiles implements runner.Adapter: a new test file maps to
// a class-name entry the Action turns into a --filter regex.
func (Adapter) EntriesForNewTestFiles(files []string) []string {
	entries := map[string]bool{}
	for _, f := range files {
		f = strings.ReplaceAll(f, "\\", "/")
		base := strings.TrimSuffix(f[strings.LastIndex(f, "/")+1:], ".php")
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
