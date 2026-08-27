// Package rspec implements the RunnerAdapter for the Ruby / RSpec
// ecosystem.
//
// Identity is deliberately SPEC-FILE granular, for the same reasons as the
// jest adapter: RSpec example names are free strings inside describe blocks
// (no stable FQN), and per-file selection preserves before(:all)/let/fixture
// semantics. The canonical test ID is the spec file's repo-relative path,
// e.g. "spec/calc_spec.rb"; canonical and native IDs are identical. Rollups
// therefore track spec files, not individual `it` examples.
//
// Collection inputs come from cmd/specroster-rbcover (one
// `bundle exec rspec` run per spec file with a SimpleCov bootstrap —
// SimpleCov has no per-example contexts). JUnit ingestion expects
// rspec_junit_formatter output (see NormalizeJUnit).
package rspec

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
const CoverageFormat = "specroster/rspec-cover/v1"

// Adapter is the RSpec runner adapter.
type Adapter struct{}

// Name implements runner.Adapter.
func (Adapter) Name() string { return "rspec" }

// CoverJSON is the collector's output.
type CoverJSON struct {
	Format string `json:"format"`
	// Tests: spec file (repo-relative) → source file → covered lines.
	Tests map[string]map[string][]int `json:"tests"`
}

// ParseCoverage implements runner.Adapter.
func (Adapter) ParseCoverage(r io.Reader) (*covtypes.Coverage, error) {
	var doc CoverJSON
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("rspec: parse coverage json: %w", err)
	}
	if doc.Format != CoverageFormat {
		return nil, fmt.Errorf("rspec: unexpected coverage format %q (need %s — produced by specroster-rbcover)", doc.Format, CoverageFormat)
	}
	cov := &covtypes.Coverage{LineTests: map[string]map[int][]string{}}
	for spec, files := range doc.Tests {
		spec = normalizePath(spec)
		for file, lines := range files {
			file = normalizePath(file)
			byLine := cov.LineTests[file]
			if byLine == nil {
				byLine = map[int][]string{}
				cov.LineTests[file] = byLine
			}
			for _, line := range lines {
				byLine[line] = append(byLine[line], spec)
			}
		}
	}
	return cov, nil
}

// ParseTestList implements runner.Adapter: one spec file path per line;
// canonical and native IDs are the same path.
func (Adapter) ParseTestList(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		p := normalizePath(strings.TrimSpace(sc.Text()))
		if p == "" {
			continue
		}
		out[p] = p
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("rspec: read test list: %w", err)
	}
	return out, nil
}

// InvocationArgs implements runner.Adapter: rspec accepts spec paths as
// positional arguments.
func (Adapter) InvocationArgs(nativeIDs []string) []string {
	out := make([]string, 0, len(nativeIDs))
	for _, id := range nativeIDs {
		if p := normalizePath(id); p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// NormalizeJUnit implements runner.Adapter. rspec_junit_formatter emits each
// testcase's classname as the spec path with "/" replaced by "." and the
// ".rb" extension dropped (e.g. "spec.models.user_spec" for
// spec/models/user_spec.rb), so we convert back: dots → slashes, then append
// ".rb". If classname already contains "/" (or ends in ".rb") it is treated
// as a path verbatim.
//
// LIMITATION: the dotted encoding is lossy — a directory name that itself
// contains a dot (e.g. "spec/v1.2/user_spec.rb" → "spec.v1.2.user_spec")
// cannot be reconstructed and will normalize to the wrong path. Repos with
// dotted directories under spec/ must configure their formatter to emit
// path-form classnames instead.
//
// name is ignored: every example in a file collapses onto the file identity.
func (Adapter) NormalizeJUnit(classname, _ string) string {
	if strings.Contains(classname, "/") || strings.Contains(classname, "\\") || strings.HasSuffix(classname, ".rb") {
		return normalizePath(classname)
	}
	return strings.ReplaceAll(classname, ".", "/") + ".rb"
}

// NormalizeNative implements runner.Adapter.
func (Adapter) NormalizeNative(nativeID string) string {
	return normalizePath(nativeID)
}

func normalizePath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	return strings.TrimPrefix(p, "./")
}

// Layout implements runner.Adapter. testDir is unused (spec files are
// recognized by convention wherever they live); srcDir optionally scopes a
// monorepo.
func (Adapter) Layout(srcDir, _ string) covtypes.Layout {
	return rbLayout{srcDir: srcDir}
}

type rbLayout struct{ srcDir string }

func (l rbLayout) ClassifyFile(p string) covtypes.FileKind {
	p = normalizePath(p)
	if !strings.HasSuffix(p, ".rb") {
		return covtypes.KindOther
	}
	if l.srcDir != "" && l.srcDir != "." && !underDir(p, l.srcDir) {
		return covtypes.KindOther
	}
	if isSpecPath(p) {
		return covtypes.KindTest
	}
	return covtypes.KindSource
}

// isSpecPath: *_spec.rb basenames, or anything under a top-level spec/
// directory (catches spec_helper.rb, support files) — RSpec's default
// conventions.
func isSpecPath(p string) bool {
	base := p[strings.LastIndex(p, "/")+1:]
	if strings.HasSuffix(base, "_spec.rb") {
		return true
	}
	return strings.HasPrefix(p, "spec/")
}

// TestsInTestFile: identity IS the file — a changed spec file selects
// itself (when the index knows it).
func (l rbLayout) TestsInTestFile(path string, all covtypes.TestSet) covtypes.TestSet {
	path = normalizePath(path)
	out := covtypes.TestSet{}
	if _, ok := all[path]; ok {
		out[path] = struct{}{}
	}
	return out
}

// EntriesForNewTestFiles implements runner.Adapter: new spec files run
// directly by path.
func (Adapter) EntriesForNewTestFiles(files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, normalizePath(f))
	}
	sort.Strings(out)
	return out
}

// FileEntryCovers implements runner.Adapter: a path entry covers exactly
// the spec file it names.
func (Adapter) FileEntryCovers(entry, canonical string) bool {
	return normalizePath(entry) == normalizePath(canonical)
}

func underDir(p, dir string) bool {
	dir = strings.TrimSuffix(dir, "/")
	return p == dir || strings.HasPrefix(p, dir+"/")
}
