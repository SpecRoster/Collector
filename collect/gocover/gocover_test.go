package gocover

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SpecRoster/Collector/runner/gotest"
)

// TestCollectorEndToEnd runs the collector against the fixture module in
// testdata/sample (two packages; strs deliberately calls into calc so
// cross-package coverage is observable) and checks the per-test mapping.
// Requires the `go` tool — true anywhere these tests run.
func TestCollectorEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not on PATH")
	}
	tmp := t.TempDir()
	covPath := filepath.Join(tmp, "coverage.json")
	colPath := filepath.Join(tmp, "collected.txt")

	if err := run("testdata/sample", covPath, colPath, "", false); err != nil {
		t.Fatalf("run: %v", err)
	}

	raw, err := os.ReadFile(covPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc gotest.CoverJSON
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Format != gotest.CoverageFormat {
		t.Errorf("format = %q", doc.Format)
	}
	if doc.Module != "example.com/sample" {
		t.Errorf("module = %q", doc.Module)
	}

	addCov := doc.Tests["example.com/sample/calc.TestAdd"]
	if addCov == nil {
		t.Fatalf("no coverage for TestAdd; have %v", testNames(doc))
	}
	if len(addCov["calc/calc.go"]) == 0 {
		t.Error("TestAdd does not cover calc/calc.go")
	}
	if _, ok := addCov["strs/strs.go"]; ok {
		t.Error("TestAdd must not cover strs/strs.go")
	}

	// The blast-radius-critical property: a test covering another package's
	// code is recorded (this is what -coverpkg=./... buys).
	repCov := doc.Tests["example.com/sample/strs.TestRepeat"]
	if repCov == nil {
		t.Fatalf("no coverage for TestRepeat; have %v", testNames(doc))
	}
	if len(repCov["strs/strs.go"]) == 0 {
		t.Error("TestRepeat does not cover its own package")
	}
	if len(repCov["calc/calc.go"]) == 0 {
		t.Error("TestRepeat does not record cross-package coverage of calc/calc.go")
	}

	// Statement-level precision: TestAdd and TestSub cover different lines
	// of the same file.
	subCov := doc.Tests["example.com/sample/calc.TestSub"]
	if subCov == nil {
		t.Fatal("no coverage for TestSub")
	}
	if equalInts(addCov["calc/calc.go"], subCov["calc/calc.go"]) {
		t.Error("TestAdd and TestSub cover identical lines — per-test isolation is broken")
	}

	// Collected list parses through the adapter and maps every test.
	colFile, err := os.Open(colPath)
	if err != nil {
		t.Fatal(err)
	}
	defer colFile.Close()
	mapping, err := (gotest.Adapter{}).ParseTestList(colFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(mapping) != 3 {
		t.Errorf("collected %d tests, want 3: %v", len(mapping), mapping)
	}
	if mapping["example.com/sample/calc.TestAdd"] != "example.com/sample/calc::TestAdd" {
		t.Errorf("mapping = %v", mapping)
	}
}

func testNames(doc gotest.CoverJSON) []string {
	out := make([]string, 0, len(doc.Tests))
	for k := range doc.Tests {
		out = append(out, k)
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The contract incremental snapshots rest on, for Go: collect a subset, but
// still report every test that exists. If the inventory shrank to the subset,
// the server would read every test outside it as deleted and erase it.
func TestOnlyCollectsSubsetButReportsFullInventory(t *testing.T) {
	tmp := t.TempDir()
	covPath := filepath.Join(tmp, "coverage.json")
	colPath := filepath.Join(tmp, "collected.txt")
	invPath := filepath.Join(tmp, "inventory.txt")

	// First, learn the inventory without collecting anything.
	if err := run("testdata/sample", covPath, invPath, "", true); err != nil {
		t.Fatalf("list-only: %v", err)
	}
	if _, err := os.Stat(covPath); err == nil {
		t.Error("a list-only pass wrote coverage; it must not look like it collected")
	}
	inv := strings.Fields(strings.TrimSpace(readFile(t, invPath)))
	if len(inv) < 2 {
		t.Fatalf("inventory = %v, want the whole sample suite", inv)
	}

	// Now collect exactly one of them.
	target := strings.ReplaceAll(inv[0], "::", ".")
	onlyPath := filepath.Join(tmp, "only.txt")
	if err := os.WriteFile(onlyPath, []byte(target+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run("testdata/sample", covPath, colPath, onlyPath, false); err != nil {
		t.Fatalf("subset run: %v", err)
	}

	var doc gotest.CoverJSON
	if err := json.Unmarshal([]byte(readFile(t, covPath)), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Tests) != 1 {
		t.Errorf("collected %d tests, want 1: %v", len(doc.Tests), doc.Tests)
	}
	if _, ok := doc.Tests[target]; !ok {
		t.Errorf("coverage does not contain the requested test %q", target)
	}
	if got := len(strings.Fields(strings.TrimSpace(readFile(t, colPath)))); got != len(inv) {
		t.Errorf("inventory shrank to %d entries, want all %d — a subset run must still report the whole suite", got, len(inv))
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
