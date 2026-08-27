// Package rustcover collects PER-TEST coverage for a Rust
// workspace — the collection half of the cargo RunnerAdapter. Rust's
// coverage tooling (cargo-llvm-cov) has no per-test contexts, so the
// collector runs each test in its own `cargo llvm-cov test` invocation with
// `-- --exact <testpath>` and parses the resulting LCOV. --workspace keeps
// cross-crate coverage visible, which is what makes the blast radius
// correct when an integration test exercises code in another file/crate.
//
// Cost: one `cargo llvm-cov test` execution per test. Instrumented builds
// are cached after the first run, so this is test runtime + per-process
// overhead — fine for small/medium suites run nightly.
//
// Requires the cargo-llvm-cov subcommand:
//
//	cargo +stable install cargo-llvm-cov
//
// Output: the cargo adapter's coverage JSON (specroster/rust-cover/v1) and
// a collected list of libtest test paths ("module::path::test_name"), one
// per line, verbatim as `cargo test -- --list` prints them.
//
// Usage:
//
//	specroster-rustcover [-dir .] [-repo-root <dir>] [-o coverage.json] [-collected collected.txt]
package rustcover

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/SpecRoster/Collector/covtypes"
	"github.com/SpecRoster/Collector/runner/cargo"
)

// Run collects per-test coverage for the cargo workspace at dir. An empty
// repoRoot defaults to dir.
func Run(dir, repoRoot, out, collected string) error {
	if repoRoot == "" {
		repoRoot = dir
	}
	return run(dir, repoRoot, out, collected)
}

func run(dir, repoRoot, outPath, collectedPath string) error {
	if err := preflight(dir); err != nil {
		return err
	}
	repoAbs, err := filepath.Abs(repoRoot)
	if err != nil {
		return fmt.Errorf("resolve repo root: %w", err)
	}
	tests, err := listTests(dir)
	if err != nil {
		return fmt.Errorf("list tests: %w", err)
	}

	layout := cargo.Adapter{}.Layout("", "")
	doc := cargo.CoverJSON{Format: cargo.CoverageFormat, Module: filepath.Base(repoAbs), Tests: map[string]map[string][]int{}}

	for _, test := range tests {
		files, err := coverOneTest(dir, repoAbs, test, layout)
		if err != nil {
			return fmt.Errorf("cover %s: %w", test, err)
		}
		if len(files) > 0 {
			doc.Tests[test] = files
		}
		fmt.Fprintf(os.Stderr, "covered %s (%d files)\n", test, len(files))
	}

	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(doc); err != nil {
		return err
	}
	return os.WriteFile(collectedPath, []byte(strings.Join(tests, "\n")+"\n"), 0o644)
}

// preflight verifies the cargo-llvm-cov subcommand is installed.
func preflight(dir string) error {
	cmd := exec.Command("cargo", "llvm-cov", "--version")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cargo-llvm-cov is required but not available (install it with `cargo +stable install cargo-llvm-cov`): %w\n%s", err, out)
	}
	return nil
}

// listTests enumerates every libtest test path in the workspace. Output
// lines look like "path::to::test: test"; entries ending ": test" are
// tests (doc-test entries contain spaces in the path and are skipped).
func listTests(dir string) ([]string, error) {
	cmd := exec.Command("cargo", "test", "--workspace", "--", "--list", "--format", "terse")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("cargo test -- --list: %w\n%s", err, ee.Stderr)
		}
		return nil, err
	}
	seen := map[string]bool{}
	var tests []string
	for _, line := range strings.Split(string(out), "\n") {
		path, ok := strings.CutSuffix(strings.TrimSpace(line), ": test")
		if !ok {
			continue
		}
		path = strings.TrimSpace(path)
		if path == "" || strings.ContainsAny(path, " \t") || seen[path] {
			continue
		}
		seen[path] = true
		tests = append(tests, path)
	}
	sort.Strings(tests)
	return tests, nil
}

// coverOneTest runs a single test with its own LCOV export and returns
// repo-relative file → covered lines.
func coverOneTest(dir, repoAbs, test string, layout covtypes.Layout) (map[string][]int, error) {
	lcov, err := os.CreateTemp("", "trcover-*.lcov")
	if err != nil {
		return nil, err
	}
	lcov.Close()
	defer os.Remove(lcov.Name())

	cmd := exec.Command("cargo", "llvm-cov", "test", "--workspace",
		"--lcov", "--output-path", lcov.Name(),
		"--", "--exact", test)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("cargo llvm-cov test: %w\n%s", err, out)
	}
	return parseLCOV(lcov.Name(), repoAbs, layout)
}

// parseLCOV reads an LCOV trace: each file record starts with SF:<path>,
// carries DA:<line>,<hits> entries (covered = hits > 0), and ends with
// end_of_record. Paths may be absolute or repo-relative; both are resolved
// against the repo root. Files outside the repo, and files the adapter
// layout classifies as non-source (integration tests, benches, non-.rs),
// are skipped.
func parseLCOV(path, repoAbs string, layout covtypes.Layout) (map[string][]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lineSets := map[string]map[int]bool{}
	cur := "" // current repo-relative file, "" while skipping
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "SF:"):
			cur = relSourcePath(strings.TrimPrefix(line, "SF:"), repoAbs, layout)
		case line == "end_of_record":
			cur = ""
		case cur != "" && strings.HasPrefix(line, "DA:"):
			// DA:<line>,<hits>[,<checksum>]
			fields := strings.Split(strings.TrimPrefix(line, "DA:"), ",")
			if len(fields) < 2 {
				continue
			}
			ln, err1 := strconv.Atoi(fields[0])
			hits, err2 := strconv.Atoi(fields[1])
			if err1 != nil || err2 != nil || hits == 0 {
				continue
			}
			set := lineSets[cur]
			if set == nil {
				set = map[int]bool{}
				lineSets[cur] = set
			}
			set[ln] = true
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

// relSourcePath resolves an LCOV SF: path to a repo-relative slash path, or
// "" when the file is outside the repo or not product source.
func relSourcePath(p, repoAbs string, layout covtypes.Layout) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(repoAbs, p)
		if err != nil {
			return ""
		}
		p = rel
	}
	p = filepath.ToSlash(filepath.Clean(p))
	if p == ".." || strings.HasPrefix(p, "../") {
		return ""
	}
	if layout.ClassifyFile(p) != covtypes.KindSource {
		return ""
	}
	return p
}
