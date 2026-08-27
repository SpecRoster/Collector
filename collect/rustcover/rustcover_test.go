package rustcover

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/SpecRoster/Collector/runner/cargo"
)

// TestCollectorEndToEnd runs the collector against the fixture crate in
// testdata/sample (greet_test deliberately exercises src/lib.rs AND
// src/text.rs so cross-file coverage is observable) and checks the per-test
// mapping. Requires cargo plus the cargo-llvm-cov subcommand; skips
// otherwise, and in -short mode (instrumented builds are slow).
func TestCollectorEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping rust coverage collection in short mode")
	}
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("cargo not on PATH")
	}
	if err := exec.Command("cargo", "llvm-cov", "--version").Run(); err != nil {
		t.Skip("cargo-llvm-cov not installed (cargo +stable install cargo-llvm-cov)")
	}
	tmp := t.TempDir()
	covPath := filepath.Join(tmp, "coverage.json")
	colPath := filepath.Join(tmp, "collected.txt")

	if err := run("testdata/sample", "testdata/sample", covPath, colPath); err != nil {
		t.Fatalf("run: %v", err)
	}

	raw, err := os.ReadFile(covPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc cargo.CoverJSON
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Format != cargo.CoverageFormat {
		t.Errorf("format = %q", doc.Format)
	}
	if doc.Module != "sample" {
		t.Errorf("module = %q", doc.Module)
	}

	addCov := doc.Tests["calc_test::test_add"]
	if addCov == nil {
		t.Fatalf("no coverage for calc_test::test_add; have %v", testNames(doc))
	}
	if len(addCov["src/lib.rs"]) == 0 {
		t.Error("test_add does not cover src/lib.rs")
	}
	if _, ok := addCov["src/text.rs"]; ok {
		t.Error("test_add must not cover src/text.rs")
	}
	for file := range addCov {
		if filepath.Ext(file) != ".rs" {
			t.Errorf("non-source file %q in coverage", file)
		}
	}

	// The blast-radius-critical property: a test in one integration binary
	// covering multiple source files is recorded per file.
	greetCov := doc.Tests["greet_test::test_greet"]
	if greetCov == nil {
		t.Fatalf("no coverage for greet_test::test_greet; have %v", testNames(doc))
	}
	if len(greetCov["src/lib.rs"]) == 0 {
		t.Error("test_greet does not cover src/lib.rs")
	}
	if len(greetCov["src/text.rs"]) == 0 {
		t.Error("test_greet does not record cross-file coverage of src/text.rs")
	}

	// Per-test isolation: test_add and test_sub cover different lines of the
	// same file.
	subCov := doc.Tests["calc_test::test_sub"]
	if subCov == nil {
		t.Fatal("no coverage for calc_test::test_sub")
	}
	if equalInts(addCov["src/lib.rs"], subCov["src/lib.rs"]) {
		t.Error("test_add and test_sub cover identical lines — per-test isolation is broken")
	}

	// Collected list parses through the adapter and maps every test.
	colFile, err := os.Open(colPath)
	if err != nil {
		t.Fatal(err)
	}
	defer colFile.Close()
	mapping, err := (cargo.Adapter{}).ParseTestList(colFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(mapping) != 3 {
		t.Errorf("collected %d tests, want 3: %v", len(mapping), mapping)
	}
	if mapping["calc_test::test_add"] != "calc_test::test_add" {
		t.Errorf("mapping = %v", mapping)
	}
}

func testNames(doc cargo.CoverJSON) []string {
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
