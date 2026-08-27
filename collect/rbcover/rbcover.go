// Package rbcover collects PER-SPEC-FILE coverage for a Ruby /
// RSpec project — the collection half of the rspec RunnerAdapter. SimpleCov
// has no per-example contexts, so the collector runs each spec file in its
// own `bundle exec rspec` invocation with a SimpleCov bootstrap helper;
// identity is the spec file (see the rspec adapter's package comment for why
// file granularity is the right unit in Ruby).
//
// Output: the rspec adapter's coverage JSON (specroster/rspec-cover/v1) and
// a collected list of repo-relative spec paths.
//
// Usage:
//
//	specroster-rbcover [-dir .] [-repo-root <dir>] \
//	    [-o coverage.json] [-collected collected.txt]
package rbcover

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SpecRoster/Collector/covtypes"
	"github.com/SpecRoster/Collector/runner/rspec"
)

// Run collects per-spec-file coverage for the RSpec project at dir. An empty
// repoRoot defaults to dir.
func Run(dir, repoRoot, out, collected string) error {
	return run(dir, repoRoot, out, collected)
}

func run(dir, repoRoot, outPath, collectedPath string) error {
	if repoRoot == "" {
		repoRoot = dir
	}
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return err
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	specs, err := listSpecs(absDir)
	if err != nil {
		return err
	}
	if len(specs) == 0 {
		return fmt.Errorf("no *_spec.rb files found in %s", dir)
	}

	layout := rspec.Adapter{}.Layout("", "")
	doc := rspec.CoverJSON{Format: rspec.CoverageFormat, Tests: map[string]map[string][]int{}}
	var natives []string
	for _, absSpec := range specs {
		relSpec, err := filepath.Rel(absRoot, absSpec)
		if err != nil || strings.HasPrefix(relSpec, "..") {
			continue
		}
		relSpec = filepath.ToSlash(relSpec)
		natives = append(natives, relSpec)
		files, err := coverOneSpec(absDir, absSpec, absRoot, layout)
		if err != nil {
			return fmt.Errorf("cover %s: %w", relSpec, err)
		}
		if len(files) > 0 {
			doc.Tests[relSpec] = files
		}
		fmt.Fprintf(os.Stderr, "covered %s (%d files)\n", relSpec, len(files))
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

// listSpecs walks dir for *_spec.rb files (RSpec's naming convention),
// skipping dependency and VCS directories.
func listSpecs(absDir string) ([]string, error) {
	var specs []string
	skip := map[string]bool{"vendor": true, "node_modules": true, ".git": true}
	err := filepath.WalkDir(absDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), "_spec.rb") {
			specs = append(specs, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(specs)
	return specs, nil
}

// simplecovHelper is the bootstrap required (via `rspec -r`) before any spec
// or application code loads, so SimpleCov instruments everything. Output
// location and root come from the environment so the helper itself can live
// in a temp dir outside the repo.
const simplecovHelper = `require "simplecov"
SimpleCov.root ENV["TR_ROOT"]
SimpleCov.coverage_dir ENV["TR_COV_DIR"]
SimpleCov.start
`

// coverOneSpec runs one spec file under SimpleCov and returns repo-relative
// source file → covered lines. Spec/test files themselves are excluded
// (identity tracks specs; the index maps product source only).
func coverOneSpec(absDir, absSpec, absRoot string, layout covtypes.Layout) (map[string][]int, error) {
	tmp, err := os.MkdirTemp("", "trrb-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	helper := filepath.Join(tmp, "tr_simplecov_helper.rb")
	if err := os.WriteFile(helper, []byte(simplecovHelper), 0o644); err != nil {
		return nil, err
	}

	relSpec, err := filepath.Rel(absDir, absSpec)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("bundle", "exec", "rspec", "-r", helper, filepath.ToSlash(relSpec))
	cmd.Dir = absDir
	cmd.Env = append(os.Environ(), "TR_COV_DIR="+tmp, "TR_ROOT="+absRoot)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("rspec run: %w\n%s", err, out)
	}
	return parseResultset(filepath.Join(tmp, ".resultset.json"), absRoot, layout)
}

// parseResultset reads SimpleCov's .resultset.json:
//
//	{"RSpec": {"coverage": {"<abs file>": {"lines": [null, 1, 0, ...]}}}}
//
// Older SimpleCov versions put the lines array directly as the file value
// instead of {"lines": [...]} — both shapes are handled. Index i of the
// array (0-based) is line i+1; a line is covered when its hit count is an
// integer > 0; null marks non-executable lines.
func parseResultset(path, absRoot string, layout covtypes.Layout) (map[string][]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read simplecov resultset: %w", err)
	}
	var doc map[string]struct {
		Coverage map[string]json.RawMessage `json:"coverage"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse simplecov resultset: %w", err)
	}
	out := map[string][]int{}
	for _, suite := range doc {
		for absFile, raw := range suite.Coverage {
			rel, err := filepath.Rel(absRoot, absFile)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			rel = filepath.ToSlash(rel)
			if layout.ClassifyFile(rel) != covtypes.KindSource {
				continue
			}
			hits, err := decodeLines(raw)
			if err != nil {
				return nil, fmt.Errorf("parse coverage for %s: %w", rel, err)
			}
			var lines []int
			for i, h := range hits {
				if h != nil && *h > 0 {
					lines = append(lines, i+1)
				}
			}
			if len(lines) == 0 {
				continue
			}
			sort.Ints(lines)
			out[rel] = lines
		}
	}
	return out, nil
}

// decodeLines accepts both resultset shapes: modern {"lines": [...]} objects
// and the bare [...] array older SimpleCov emits.
func decodeLines(raw json.RawMessage) ([]*float64, error) {
	var wrapped struct {
		Lines []*float64 `json:"lines"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Lines != nil {
		return wrapped.Lines, nil
	}
	var direct []*float64
	if err := json.Unmarshal(raw, &direct); err != nil {
		return nil, err
	}
	return direct, nil
}
