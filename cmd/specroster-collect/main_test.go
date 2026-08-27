package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestDispatchRejectsBadRunner covers the two dispatch error paths; the
// per-runner happy paths are covered end-to-end by each handler's own tests
// under collect/.
func TestDispatchRejectsBadRunner(t *testing.T) {
	for _, runner := range []string{"", "vitest"} {
		err := dispatch(runner, ".", "", "o.json", "c.txt", "", "t.json", "msbuild", "", 1, "src/main/java", "vendor/bin/phpunit", "python", "")
		if err == nil {
			t.Errorf("dispatch(%q) = nil error, want error", runner)
		} else if !strings.Contains(err.Error(), "runner") {
			t.Errorf("dispatch(%q) error %q does not mention runner", runner, err)
		}
	}
}

// TestDispatchGotest exercises one real dispatch route end-to-end through
// the consolidated entrypoint (gotest, against gocover's fixture module) to
// prove the wiring, not just the error paths.
func TestDispatchGotest(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	tmp := t.TempDir()
	covPath := filepath.Join(tmp, "coverage.json")
	colPath := filepath.Join(tmp, "collected.txt")
	if err := dispatch("gotest", "../../collect/gocover/testdata/sample", "", covPath, colPath, "", "t.json", "msbuild", "", 1, "src/main/java", "vendor/bin/phpunit", "python", ""); err != nil {
		t.Fatalf("dispatch(gotest): %v", err)
	}
}
