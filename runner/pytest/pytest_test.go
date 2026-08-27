package pytest

import (
	"reflect"
	"strings"
	"testing"
)

const sampleCoverageJSON = `{
  "meta": {"format": 3},
  "files": {
    "rich/console.py": {
      "executed_lines": [1, 10, 20],
      "contexts": {
        "1": [""],
        "10": ["tests.test_console.test_print|run", "tests.test_console.test_log|run"],
        "20": ["tests.test_console.test_print|run", "tests.test_console.test_print|run"]
      }
    },
    "rich/empty.py": {
      "contexts": {"1": [""]}
    }
  }
}`

func TestParseCoverage(t *testing.T) {
	cov, err := Adapter{}.ParseCoverage(strings.NewReader(sampleCoverageJSON))
	if err != nil {
		t.Fatalf("ParseCoverage: %v", err)
	}

	lines, ok := cov.LineTests["rich/console.py"]
	if !ok {
		t.Fatalf("console.py missing; got files %v", keys(cov.LineTests))
	}
	// Line 1 has only the import-time context → dropped entirely.
	if _, ok := lines[1]; ok {
		t.Error("line 1 (import-time only) should be dropped")
	}
	want10 := []string{"test_console.test_print", "test_console.test_log"}
	if !reflect.DeepEqual(lines[10], want10) {
		t.Errorf("line 10 = %v, want %v (normalized, package prefix dropped)", lines[10], want10)
	}
	// Duplicate contexts collapse.
	if !reflect.DeepEqual(lines[20], []string{"test_console.test_print"}) {
		t.Errorf("line 20 = %v, want deduped single test", lines[20])
	}
	// File with no test contexts is dropped.
	if _, ok := cov.LineTests["rich/empty.py"]; ok {
		t.Error("empty.py (no test contexts) should be dropped")
	}
}

func TestParseCoverageRejectsNonCoverageJSON(t *testing.T) {
	if _, err := (Adapter{}).ParseCoverage(strings.NewReader(`{"hello": 1}`)); err == nil {
		t.Error("want error for JSON without files section")
	}
	if _, err := (Adapter{}).ParseCoverage(strings.NewReader(`not json`)); err == nil {
		t.Error("want error for non-JSON input")
	}
}

const sampleCollectOutput = `tests/test_console.py::test_print
tests/test_console.py::test_color[red]
tests/test_console.py::test_color[blue]
tests/test_style.py::TestStyle::test_parse

4 tests collected in 0.12s
`

func TestParseTestList(t *testing.T) {
	m, err := Adapter{}.ParseTestList(strings.NewReader(sampleCollectOutput))
	if err != nil {
		t.Fatalf("ParseTestList: %v", err)
	}
	want := map[string]string{
		"test_console.test_print":         "tests/test_console.py::test_print",
		"test_console.test_color":         "tests/test_console.py::test_color[red]", // first param wins
		"test_style.TestStyle.test_parse": "tests/test_style.py::TestStyle::test_parse",
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("ParseTestList = %v, want %v", m, want)
	}
}

func TestInvocationArgs(t *testing.T) {
	ids := []string{"tests/test_a.py::test_x", "tests/test_b.py::TestC::test_y"}
	got := Adapter{}.InvocationArgs(ids)
	if !reflect.DeepEqual(got, ids) {
		t.Errorf("InvocationArgs = %v", got)
	}
	got[0] = "mutated"
	if ids[0] == "mutated" {
		t.Error("InvocationArgs must copy, not alias, its input")
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
