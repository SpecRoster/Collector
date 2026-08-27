// Package covtypes is the shared vocabulary between runner adapters and the
// Blast Radius engine: the coverage shape adapters produce, the file
// classification they supply, and the test-identity set both sides speak in.
//
// It is the seam between the open collector and the closed engine. Both
// sides depend on this package; neither depends on the other. The engine
// (reverse index, floor, budget-fill, ranking) lives in a separate private
// module and imports this one — never the reverse.
//
// If you find yourself wanting the engine from an adapter or a collector,
// the shared piece belongs here instead.
package covtypes

import (
	"sort"
	"strings"
)

// TestSet is a set of canonical test IDs (see internal/testid).
type TestSet map[string]struct{}

// Sorted returns the members in lexical order.
func (s TestSet) Sorted() []string {
	out := make([]string, 0, len(s))
	for t := range s {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Add inserts a canonical test ID into the set.
func (s TestSet) Add(t string) { s[t] = struct{}{} }

// Coverage is runner-agnostic per-test coverage: which canonical tests
// executed each line of each repo-relative file. Runner adapters
// (runner) produce this from their native coverage formats.
type Coverage struct {
	// LineTests maps repo-relative file path → line number → canonical test
	// IDs that executed the line.
	LineTests map[string]map[int][]string
}

// FileKind classifies a repo file for selection purposes.
type FileKind int

const (
	// KindOther: docs, config, CI — changes select nothing.
	KindOther FileKind = iota
	// KindSource: product code — changes select via the reverse index.
	KindSource
	// KindTest: test code — changes select the file's own tests by name.
	KindTest
)

// Layout is how a runner ecosystem organizes files and maps test files to
// test identities. It is adapter-supplied: pytest keeps tests in a tests/
// directory and keys identity on the file basename; Go keeps _test.go files
// next to source and keys identity on the package.
type Layout interface {
	ClassifyFile(path string) FileKind
	// TestsInTestFile returns the canonical IDs in `all` that belong to the
	// given (changed) test file.
	TestsInTestFile(path string, all TestSet) TestSet
}

// DirLayout is the directory-split layout (pytest-style): source under
// SrcDir, tests under TestDir, files matched by Suffix, and test-file
// membership by file-basename prefix (canonical IDs are
// <file-basename>.<qualname>).
type DirLayout struct {
	SrcDir  string
	TestDir string
	Suffix  string
}

// ClassifyFile implements Layout.
func (l DirLayout) ClassifyFile(p string) FileKind {
	p = strings.ReplaceAll(p, "\\", "/")
	if !strings.HasSuffix(p, l.Suffix) {
		return KindOther
	}
	switch {
	case underDir(p, l.TestDir):
		return KindTest
	case underDir(p, l.SrcDir):
		return KindSource
	default:
		return KindOther
	}
}

// TestsInTestFile implements Layout: 'tests/test_basic.py' → canonical keys
// starting 'test_basic.'.
func (l DirLayout) TestsInTestFile(path string, all TestSet) TestSet {
	path = strings.ReplaceAll(path, "\\", "/")
	base := path[strings.LastIndex(path, "/")+1:]
	prefix := strings.TrimSuffix(base, l.Suffix) + "."
	out := TestSet{}
	for t := range all {
		if strings.HasPrefix(t, prefix) {
			out.Add(t)
		}
	}
	return out
}

// underDir reports whether repo-relative path p is dir itself or inside it.
func underDir(p, dir string) bool {
	p = strings.ReplaceAll(p, "\\", "/")
	dir = strings.TrimSuffix(strings.ReplaceAll(dir, "\\", "/"), "/")
	return p == dir || strings.HasPrefix(p, dir+"/")
}
