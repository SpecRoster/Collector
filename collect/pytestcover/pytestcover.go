// Package pytestcover collects PER-TEST coverage for a pytest project —
// the collection half of the pytest RunnerAdapter. Unlike every other
// ecosystem, coverage.py already records per-test coverage natively
// (dynamic contexts), so this handler is a thin orchestrator: it makes
// sure dynamic_context is configured, runs the suite under coverage.py,
// and exports the raw `coverage json --show-contexts` document. The JSON
// is uploaded verbatim and parsed server-side by the pytest adapter —
// no client-side transformation.
//
// Ported from the former pytest branch of action/coverage/action.yml.
package pytestcover

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Run executes the pytest suite at dir under coverage.py and writes the raw
// coverage JSON to out and the collected test list to collected. python is
// the interpreter to use ("" defaults to "python"); pytestArgs is a
// whitespace-split extra-argument string (matching the action's historical
// unquoted $PYTEST_ARGS expansion).
func Run(dir, python, pytestArgs, out, collected string) error {
	if python == "" {
		python = "python"
	}
	outAbs, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	colAbs, err := filepath.Abs(collected)
	if err != nil {
		return err
	}

	env := os.Environ()
	if needsRCFile(dir) {
		rc, err := os.CreateTemp("", "trcoveragerc-*")
		if err != nil {
			return err
		}
		defer os.Remove(rc.Name())
		if _, err := rc.WriteString("[run]\ndynamic_context = test_function\n"); err != nil {
			rc.Close()
			return err
		}
		rc.Close()
		env = append(env, "COVERAGE_RCFILE="+rc.Name())
	}

	// Per-test dynamic contexts are what make the reverse index possible.
	runArgs := append([]string{"-m", "coverage", "run", "-m", "pytest"}, strings.Fields(pytestArgs)...)
	if err := runCmd(dir, env, python, runArgs...); err != nil {
		return fmt.Errorf("coverage run: %w", err)
	}
	if err := runCmd(dir, env, python, "-m", "coverage", "json", "--show-contexts", "-o", outAbs); err != nil {
		return fmt.Errorf("coverage json: %w", err)
	}

	// Collection errors are tolerated (the action's historical `|| true`):
	// the coverage document is the payload; the collected list is best-effort.
	var buf bytes.Buffer
	col := exec.Command(python, "-m", "pytest", "--collect-only", "-q")
	col.Dir = dir
	col.Env = env
	col.Stdout = &buf
	col.Stderr = os.Stderr
	_ = col.Run()
	return os.WriteFile(colAbs, buf.Bytes(), 0o644)
}

// needsRCFile reports whether none of the project's coverage config files
// declare a dynamic_context, in which case Run points COVERAGE_RCFILE at a
// synthesized rcfile enabling test_function contexts. (The old bash version
// also synthesized when pyproject.toml alone configured it — checking all
// three files before overriding is the intended behavior.)
func needsRCFile(dir string) bool {
	for _, name := range []string{".coveragerc", "setup.cfg", "pyproject.toml"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil && bytes.Contains(data, []byte("dynamic_context")) {
			return false
		}
	}
	return true
}

func runCmd(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
