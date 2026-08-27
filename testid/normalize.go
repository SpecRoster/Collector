// Package testid canonicalizes test identifiers across the formats a runner
// ecosystem emits, so coverage contexts, collected test lists, and JUnit
// results all reconcile to the same key.
//
// The normalizer was validated against real projects, where it hit id-format
// mismatches twice: click's
// coverage contexts label tests `test_x.test_y` while rich's tests/ directory
// is a package, yielding `tests.test_x.test_y` — and pytest nodeids carry the
// file path (`tests/test_x.py::test_y`). Every new runner is expected to
// surface its own quirks; normalization is the contract that absorbs them.
package testid

import (
	"regexp"
	"strings"
)

var paramRe = regexp.MustCompile(`\[.*?\]`)

// Normalize canonicalizes a coverage context or a pytest nodeid to a
// comparable key of the form <test-file-basename>.<qualname>.
//
//	"test_basic.TestC.test_y|run"            → "test_basic.TestC.test_y"
//	"tests/test_basic.py::TestC::test_y[p1]" → "test_basic.TestC.test_y"
//	"tests.test_basic.TestC.test_y"          → "test_basic.TestC.test_y"
func Normalize(testID string) string {
	// Coverage contexts carry a "|run" / "|setup" suffix.
	t := strings.TrimSpace(strings.SplitN(testID, "|", 2)[0])
	// Parametrized cases collapse onto their test function.
	t = paramRe.ReplaceAllString(t, "")

	var key string
	if i := strings.Index(t, "::"); i >= 0 {
		// pytest nodeid: reduce the path to the file basename.
		path := strings.ReplaceAll(t[:i], "\\", "/")
		base := path[strings.LastIndex(path, "/")+1:]
		base = strings.TrimSuffix(base, ".py")
		key = base + "." + strings.ReplaceAll(t[i+2:], "::", ".")
	} else {
		// Coverage context: already module.qualname.
		key = t
	}

	// Drop leading package/dir components (e.g. "tests." when tests/ is a
	// package) so both formats canonicalize to <file>.<qualname>.
	var parts []string
	for _, p := range strings.Split(key, ".") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	for i, p := range parts {
		if strings.HasPrefix(p, "test_") || strings.HasPrefix(p, "Test") {
			parts = parts[i:]
			break
		}
	}
	return strings.Join(parts, ".")
}
