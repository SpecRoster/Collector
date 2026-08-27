// Package gocover collects PER-TEST coverage for a Go module —
// the collection half of the gotest RunnerAdapter. Go's coverage tooling
// has no equivalent of coverage.py's dynamic contexts, so the collector
// runs each top-level Test function in its own `go test -run` invocation
// with -coverprofile and -coverpkg=./... (cross-package coverage is what
// makes the blast radius correct when a test in package A exercises code
// in package B).
//
// Cost: one `go test` execution per test. Builds are cached after the first
// run per package, so this is test runtime + small per-process overhead —
// fine for small/medium suites run nightly; per-package batching is the
// planned optimization for large ones.
//
// Output: the gotest adapter's coverage JSON (specroster/go-cover/v1) and
// a collected list of "pkg::TestName" native IDs.
//
// Usage:
//
//	specroster-gocover [-dir .] [-o coverage.json] [-collected collected.txt]
package gocover

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/SpecRoster/Collector/runner/gotest"
)

// Run collects per-test coverage for the Go module at dir, writing the
// coverage JSON to out and the collected test list to collected.
func Run(dir, out, collected string) error {
	return run(dir, out, collected)
}

func run(dir, outPath, collectedPath string) error {
	module, err := modulePath(dir)
	if err != nil {
		return err
	}
	pkgs, err := goLines(dir, "list", "./...")
	if err != nil {
		return fmt.Errorf("go list: %w", err)
	}

	doc := gotest.CoverJSON{Format: gotest.CoverageFormat, Module: module, Tests: map[string]map[string][]int{}}
	var natives []string

	for _, pkg := range pkgs {
		tests, err := listTests(dir, pkg)
		if err != nil {
			return fmt.Errorf("list tests in %s: %w", pkg, err)
		}
		for _, test := range tests {
			natives = append(natives, pkg+"::"+test)
			files, err := coverOneTest(dir, pkg, test, module)
			if err != nil {
				return fmt.Errorf("cover %s.%s: %w", pkg, test, err)
			}
			if len(files) > 0 {
				doc.Tests[pkg+"."+test] = files
			}
			fmt.Fprintf(os.Stderr, "covered %s.%s (%d files)\n", pkg, test, len(files))
		}
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

func modulePath(dir string) (string, error) {
	lines, err := goLines(dir, "list", "-m")
	if err != nil || len(lines) == 0 {
		return "", fmt.Errorf("resolve module path (is %s a Go module?): %w", dir, err)
	}
	return lines[0], nil
}

var testNameRe = regexp.MustCompile(`^Test\w*$`)

// listTests returns the package's top-level Test functions.
func listTests(dir, pkg string) ([]string, error) {
	lines, err := goLines(dir, "test", "-list", "^Test", pkg)
	if err != nil {
		return nil, err
	}
	var tests []string
	for _, l := range lines {
		if testNameRe.MatchString(l) {
			tests = append(tests, l)
		}
	}
	return tests, nil
}

// coverOneTest runs a single test with its own coverage profile and returns
// repo-relative file → covered lines.
func coverOneTest(dir, pkg, test, module string) (map[string][]int, error) {
	profile, err := os.CreateTemp("", "trcover-*.out")
	if err != nil {
		return nil, err
	}
	profile.Close()
	defer os.Remove(profile.Name())

	cmd := exec.Command("go", "test", "-count=1",
		"-run", "^"+regexp.QuoteMeta(test)+"$",
		"-coverprofile", profile.Name(), "-coverpkg", "./...", pkg)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("go test: %w\n%s", err, out)
	}
	return parseProfile(profile.Name(), module)
}

// parseProfile reads a go cover profile, keeping executed (count>0) lines
// of non-test files, with paths made repo-relative by stripping the module
// prefix.
func parseProfile(path, module string) (map[string][]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lineSets := map[string]map[int]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		// <file>:<startLine>.<startCol>,<endLine>.<endCol> <numStmt> <count>
		file, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) != 3 {
			continue
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil || count == 0 {
			continue
		}
		rel := strings.TrimPrefix(file, module+"/")
		if strings.HasSuffix(rel, "_test.go") {
			continue
		}
		startStr, endStr, ok := strings.Cut(fields[0], ",")
		if !ok {
			continue
		}
		start, err1 := strconv.Atoi(strings.SplitN(startStr, ".", 2)[0])
		end, err2 := strconv.Atoi(strings.SplitN(endStr, ".", 2)[0])
		if err1 != nil || err2 != nil {
			continue
		}
		set := lineSets[rel]
		if set == nil {
			set = map[int]bool{}
			lineSets[rel] = set
		}
		for l := start; l <= end; l++ {
			set[l] = true
		}
	}
	out := map[string][]int{}
	for file, set := range lineSets {
		lines := make([]int, 0, len(set))
		for l := range set {
			lines = append(lines, l)
		}
		sort.Ints(lines)
		out[file] = lines
	}
	return out, nil
}

func goLines(dir string, args ...string) ([]string, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, ee.Stderr)
		}
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}
