package pytestcover

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SpecRoster/Collector/runner/pytest"
)

func TestNeedsRCFile(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  bool
	}{
		{"no config files", nil, true},
		{"coveragerc without contexts", map[string]string{".coveragerc": "[run]\nbranch = true\n"}, true},
		{"coveragerc with contexts", map[string]string{".coveragerc": "[run]\ndynamic_context = test_function\n"}, false},
		{"setup.cfg with contexts", map[string]string{"setup.cfg": "[coverage:run]\ndynamic_context = test_function\n"}, false},
		// The old bash detection clobbered a pyproject-only config; the
		// Go port must respect it.
		{"pyproject with contexts", map[string]string{"pyproject.toml": "[tool.coverage.run]\ndynamic_context = \"test_function\"\n"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := needsRCFile(dir); got != tc.want {
				t.Errorf("needsRCFile = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCollectorEndToEnd runs the handler against the fixture project in
// testdata/sample (greet.py calls into calc.py so cross-file coverage is
// observable) and round-trips the outputs through the real pytest adapter —
// the same parse the server performs on upload. Requires python with pytest
// and coverage installed.
func TestCollectorEndToEnd(t *testing.T) {
	python := pythonWith(t, "pytest", "coverage")
	if testing.Short() {
		t.Skip("short mode")
	}

	tmp := t.TempDir()
	covPath := filepath.Join(tmp, "coverage.json")
	colPath := filepath.Join(tmp, "collected.txt")
	if err := Run("testdata/sample", python, "", covPath, colPath); err != nil {
		t.Fatalf("Run: %v", err)
	}

	covFile, err := os.Open(covPath)
	if err != nil {
		t.Fatal(err)
	}
	defer covFile.Close()
	cov, err := pytest.Adapter{}.ParseCoverage(covFile)
	if err != nil {
		t.Fatalf("adapter ParseCoverage: %v", err)
	}
	// test_greet must cover calc.py (cross-file blast radius).
	sawCrossFile := false
	for file, lines := range cov.LineTests {
		if filepath.Base(file) != "calc.py" {
			continue
		}
		for _, tests := range lines {
			for _, test := range tests {
				if strings.Contains(test, "test_greeting_length") {
					sawCrossFile = true
				}
			}
		}
	}
	if !sawCrossFile {
		t.Errorf("expected test_greeting_length to cover calc.py; covered files: %v", keys(cov.LineTests))
	}

	colFile, err := os.Open(colPath)
	if err != nil {
		t.Fatal(err)
	}
	defer colFile.Close()
	list, err := pytest.Adapter{}.ParseTestList(colFile)
	if err != nil {
		t.Fatalf("adapter ParseTestList: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("collected %d tests, want 3: %v", len(list), list)
	}
}

// pythonWith locates a python interpreter that can import all the given
// modules, or skips the test.
func pythonWith(t *testing.T, modules ...string) string {
	t.Helper()
	for _, python := range []string{"python", "python3"} {
		if _, err := exec.LookPath(python); err != nil {
			continue
		}
		ok := true
		for _, m := range modules {
			if err := exec.Command(python, "-c", "import "+m).Run(); err != nil {
				ok = false
				break
			}
		}
		if ok {
			return python
		}
	}
	t.Skipf("no python with %v on PATH", modules)
	return ""
}

func keys[M ~map[string]V, V any](m M) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
