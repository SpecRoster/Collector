// Package pytest implements the RunnerAdapter for the
// Python / pytest / coverage.py ecosystem — the first supported runner,
// validated end-to-end by the Step-0 spike.
//
// Collection inputs:
//   - per-test coverage: `coverage json --show-contexts` output, produced
//     from a run with dynamic_context = test_function configured
//   - test inventory: `pytest --collect-only -q` output (one nodeid per line)
package pytest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/SpecRoster/Collector/covtypes"
	"github.com/SpecRoster/Collector/testid"
)

// Adapter is the pytest runner adapter.
type Adapter struct{}

// Name implements runner.Adapter.
func (Adapter) Name() string { return "pytest" }

// coverageJSON is the subset of `coverage json --show-contexts` we consume.
type coverageJSON struct {
	Files map[string]struct {
		// Contexts: line number (as string) → coverage contexts that
		// executed it. The empty context "" is import-time execution.
		Contexts map[string][]string `json:"contexts"`
	} `json:"files"`
}

// ParseCoverage implements runner.Adapter.
func (Adapter) ParseCoverage(r io.Reader) (*covtypes.Coverage, error) {
	var doc coverageJSON
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("pytest: parse coverage json: %w", err)
	}
	if doc.Files == nil {
		return nil, fmt.Errorf("pytest: coverage json has no files section (need `coverage json --show-contexts` output)")
	}

	cov := &covtypes.Coverage{LineTests: map[string]map[int][]string{}}
	for file, data := range doc.Files {
		file = strings.ReplaceAll(file, "\\", "/")
		lines := map[int][]string{}
		for lineStr, ctxs := range data.Contexts {
			line, err := strconv.Atoi(lineStr)
			if err != nil {
				return nil, fmt.Errorf("pytest: bad line number %q in %s: %w", lineStr, file, err)
			}
			seen := map[string]struct{}{}
			var tests []string
			for _, c := range ctxs {
				if c == "" {
					continue // import-time execution, not a test
				}
				key := testid.Normalize(c)
				if key == "" {
					continue
				}
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				tests = append(tests, key)
			}
			if len(tests) > 0 {
				lines[line] = tests
			}
		}
		if len(lines) > 0 {
			cov.LineTests[file] = lines
		}
	}
	return cov, nil
}

// ParseTestList implements runner.Adapter. It reads `pytest --collect-only -q`
// output and maps canonical IDs to pytest nodeids. When several nodeids
// normalize to one canonical ID (parametrized cases), the first wins — all
// parametrizations of a test share one identity.
func (Adapter) ParseTestList(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		node := strings.TrimSpace(sc.Text())
		if !strings.Contains(node, "::") {
			continue // summary lines, blank lines, warnings
		}
		key := testid.Normalize(node)
		if key == "" {
			continue
		}
		if _, ok := out[key]; !ok {
			out[key] = node
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("pytest: read test list: %w", err)
	}
	return out, nil
}

// InvocationArgs implements runner.Adapter. pytest accepts nodeids as
// positional arguments. Parametrized selections run the whole test function
// (nodeids from ParseTestList are param-free firsts; running a parent id
// runs all its parametrizations — conservative and correct).
func (Adapter) InvocationArgs(nativeIDs []string) []string {
	return append([]string{}, nativeIDs...)
}

// NormalizeJUnit implements runner.Adapter: pytest junitxml emits
// classname = dotted module path, name = test function (maybe [params]).
func (Adapter) NormalizeJUnit(classname, name string) string {
	id := name
	if classname != "" {
		id = classname + "." + name
	}
	return testid.Normalize(id)
}

// NormalizeNative implements runner.Adapter for pytest nodeids.
func (Adapter) NormalizeNative(nativeID string) string {
	return testid.Normalize(nativeID)
}

// Layout implements runner.Adapter: source under srcDir, tests under
// testDir, .py files, identity keyed on the test file basename.
func (Adapter) Layout(srcDir, testDir string) covtypes.Layout {
	return covtypes.DirLayout{SrcDir: srcDir, TestDir: testDir, Suffix: ".py"}
}

// EntriesForNewTestFiles implements runner.Adapter: pytest runs files
// directly, so the file paths themselves are the manifest entries.
func (Adapter) EntriesForNewTestFiles(files []string) []string {
	return append([]string{}, files...)
}

// FileEntryCovers implements runner.Adapter: a file entry covers every test
// whose canonical key starts with the file's basename.
func (Adapter) FileEntryCovers(entry, canonical string) bool {
	if !strings.HasSuffix(entry, ".py") {
		return false
	}
	base := entry[strings.LastIndex(entry, "/")+1:]
	return strings.HasPrefix(canonical, strings.TrimSuffix(base, ".py")+".")
}
