// Package jvmcover collects PER-TEST coverage for a Maven /
// JUnit 5 project — the collection half of the junit RunnerAdapter
// (Maven-first; Gradle later). Like Go and .NET, the JVM has no per-test
// coverage contexts, so the collector runs each test in its own
// `mvn test -Dtest=Class#method` invocation with the JaCoCo agent attached
// (the project's pom must bind jacoco-maven-plugin's prepare-agent goal).
//
// Output: the junit adapter's coverage JSON (specroster/jvm-cover/v1)
// and a collected list of "package.Class::method" native IDs.
//
// Usage:
//
//	specroster-jvmcover [-dir path/to/maven/project] [-repo-root .] \
//	    [-source-root src/main/java] [-o coverage.json] [-collected collected.txt]
package jvmcover

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SpecRoster/Collector/runner/junit5"
)

// Run collects per-test coverage for the Maven project at dir. An empty
// repoRoot defaults to dir; sourceRoot is the root JaCoCo package paths are
// joined under (conventionally src/main/java).
func Run(dir, repoRoot, sourceRoot, out, collected string) error {
	return run(dir, repoRoot, sourceRoot, out, collected)
}

// testCase is one enumerated Surefire testcase.
type testCase struct{ class, method string }

func run(dir, repoRoot, sourceRoot, outPath, collectedPath string) error {
	if repoRoot == "" {
		repoRoot = dir
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return err
	}
	relDir, err := filepath.Rel(absRoot, absDir)
	if err != nil {
		return err
	}
	pathPrefix := filepath.ToSlash(relDir)
	if pathPrefix == "." {
		pathPrefix = ""
	}

	// One full run up front to enumerate tests from Surefire's reports.
	if out, err := mvn(absDir, "-q", "test").CombinedOutput(); err != nil {
		return fmt.Errorf("mvn test: %w\n%s", err, out)
	}
	tests, err := listTests(absDir)
	if err != nil {
		return err
	}
	if len(tests) == 0 {
		return fmt.Errorf("no tests found in %s", filepath.Join(absDir, "target", "surefire-reports"))
	}

	doc := junit5.CoverJSON{Format: junit5.CoverageFormat, Tests: map[string]map[string][]int{}}
	var natives []string
	for _, tc := range tests {
		natives = append(natives, tc.class+"::"+tc.method)
		files, err := coverOneTest(absDir, tc, sourceRoot, pathPrefix)
		if err != nil {
			return fmt.Errorf("cover %s#%s: %w", tc.class, tc.method, err)
		}
		if len(files) > 0 {
			doc.Tests[tc.class+"."+tc.method] = files
		}
		fmt.Fprintf(os.Stderr, "covered %s#%s (%d files)\n", tc.class, tc.method, len(files))
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

// mvn builds a Maven command running in dir.
func mvn(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("mvn", args...)
	cmd.Dir = dir
	return cmd
}

// surefireSuite is the slice of a Surefire TEST-*.xml report we need:
// <testcase classname="pkg.Class" name="method"/> entries.
type surefireSuite struct {
	Testcases []struct {
		Classname string `xml:"classname,attr"`
		Name      string `xml:"name,attr"`
	} `xml:"testcase"`
}

// listTests enumerates tests from target/surefire-reports/TEST-*.xml.
// Parametrized invocations ("method(int)[1]") and "method()" renderings
// collapse onto their method and dedupe.
func listTests(absDir string) ([]testCase, error) {
	reports, err := filepath.Glob(filepath.Join(absDir, "target", "surefire-reports", "TEST-*.xml"))
	if err != nil {
		return nil, err
	}
	seen := map[testCase]bool{}
	var tests []testCase
	for _, report := range reports {
		data, err := os.ReadFile(report)
		if err != nil {
			return nil, err
		}
		var suite surefireSuite
		if err := xml.Unmarshal(data, &suite); err != nil {
			return nil, fmt.Errorf("parse %s: %w", report, err)
		}
		for _, c := range suite.Testcases {
			tc := testCase{class: strings.TrimSpace(c.Classname), method: stripMethodSuffix(c.Name)}
			if tc.class == "" || tc.method == "" || seen[tc] {
				continue
			}
			seen[tc] = true
			tests = append(tests, tc)
		}
	}
	sort.Slice(tests, func(i, j int) bool {
		if tests[i].class != tests[j].class {
			return tests[i].class < tests[j].class
		}
		return tests[i].method < tests[j].method
	})
	return tests, nil
}

// stripMethodSuffix drops Surefire's "()" rendering and parametrized
// "(args)[n]" invocation suffixes from a method name.
func stripMethodSuffix(s string) string {
	if i := strings.IndexAny(s, "(["); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// coverOneTest runs a single test with JaCoCo collection and returns
// repo-relative file → covered lines.
func coverOneTest(absDir string, tc testCase, sourceRoot, pathPrefix string) (map[string][]int, error) {
	// The JaCoCo agent appends to target/jacoco.exec by default; remove it
	// so each run records only this test's execution.
	if err := os.Remove(filepath.Join(absDir, "target", "jacoco.exec")); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	cmd := mvn(absDir, "-q", "test",
		"-Dtest="+tc.class+"#"+tc.method,
		"-Dsurefire.failIfNoSpecifiedTests=false",
		"jacoco:report")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("mvn test: %w\n%s", err, out)
	}
	return parseJacoco(filepath.Join(absDir, "target", "site", "jacoco", "jacoco.xml"), sourceRoot, pathPrefix)
}

// jacocoReport is the slice of JaCoCo's XML report we need:
// <package name="com/x"><sourcefile name="A.java"><line nr="7" ci="3"/>.
type jacocoReport struct {
	Packages []struct {
		Name        string `xml:"name,attr"`
		Sourcefiles []struct {
			Name  string `xml:"name,attr"`
			Lines []struct {
				Nr int `xml:"nr,attr"`
				Ci int `xml:"ci,attr"`
			} `xml:"line"`
		} `xml:"sourcefile"`
	} `xml:"package"`
}

// parseJacoco returns repo-relative file → covered lines (ci > 0). JaCoCo
// only reports main (instrumented) classes, so no further exclusion is
// needed; file paths are <pathPrefix>/<sourceRoot>/<package>/<sourcefile>.
func parseJacoco(reportPath, sourceRoot, pathPrefix string) (map[string][]int, error) {
	data, err := os.ReadFile(reportPath)
	if err != nil {
		return nil, fmt.Errorf("read jacoco report: %w", err)
	}
	var doc jacocoReport
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse jacoco report: %w", err)
	}
	out := map[string][]int{}
	for _, pkg := range doc.Packages {
		for _, sf := range pkg.Sourcefiles {
			var lines []int
			for _, l := range sf.Lines {
				if l.Ci > 0 {
					lines = append(lines, l.Nr)
				}
			}
			if len(lines) == 0 {
				continue
			}
			sort.Ints(lines)
			rel := path.Join(pathPrefix, sourceRoot, pkg.Name, sf.Name)
			out[rel] = lines
		}
	}
	return out, nil
}
