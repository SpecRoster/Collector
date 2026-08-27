package dotnetcover

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/SpecRoster/Collector/runner/dotnet"
)

// TestCollectorEndToEnd runs the collector against the fixture solution in
// testdata/sample (Demo classlib + Demo.Tests xUnit project; Greeter calls
// into Calculator so cross-file coverage is observable). Requires the
// dotnet SDK; skipped where it's absent. NuGet restore needs network on
// first run.
func TestCollectorEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet SDK not on PATH")
	}
	if testing.Short() {
		t.Skip("short mode")
	}
	tmp := t.TempDir()
	covPath := filepath.Join(tmp, "coverage.json")
	colPath := filepath.Join(tmp, "collected.txt")
	timPath := filepath.Join(tmp, "timings.json")

	// No -project: discovery must find BOTH test projects and merge them.
	if err := run("", "testdata/sample", covPath, colPath, timPath, "msbuild", "", 2); err != nil {
		t.Fatalf("run: %v", err)
	}

	raw, err := os.ReadFile(covPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc dotnet.CoverJSON
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Format != dotnet.CoverageFormat {
		t.Errorf("format = %q", doc.Format)
	}

	addCov := doc.Tests["Demo.Tests.CalculatorTests.AddWorks"]
	if addCov == nil {
		t.Fatalf("no coverage for AddWorks; have %v", testNames(doc))
	}
	if len(addCov["Demo/Calculator.cs"]) == 0 {
		t.Error("AddWorks does not cover Demo/Calculator.cs")
	}
	if _, ok := addCov["Demo/Greeter.cs"]; ok {
		t.Error("AddWorks must not cover Greeter.cs")
	}

	greetCov := doc.Tests["Demo.Tests.GreeterTests.GreetsByCount"]
	if greetCov == nil {
		t.Fatalf("no coverage for GreetsByCount; have %v", testNames(doc))
	}
	if len(greetCov["Demo/Greeter.cs"]) == 0 {
		t.Error("GreetsByCount does not cover its own file")
	}
	if len(greetCov["Demo/Calculator.cs"]) == 0 {
		t.Error("GreetsByCount does not record cross-file coverage of Calculator.cs")
	}

	// Per-test isolation: Add and Sub cover different lines of the file.
	subCov := doc.Tests["Demo.Tests.CalculatorTests.SubWorks"]
	if subCov == nil {
		t.Fatal("no coverage for SubWorks")
	}
	if equalInts(addCov["Demo/Calculator.cs"], subCov["Demo/Calculator.cs"]) {
		t.Error("AddWorks and SubWorks cover identical lines — per-test isolation broken")
	}

	// The second project's test must be merged in (multi-project support).
	moreCov := doc.Tests["Demo.MoreTests.SubMoreTests.SubHandlesNegatives"]
	if moreCov == nil {
		t.Fatalf("no coverage for the second project's test; have %v", testNames(doc))
	}
	if len(moreCov["Demo/Calculator.cs"]) == 0 {
		t.Error("second project's test does not cover Calculator.cs")
	}

	colFile, err := os.Open(colPath)
	if err != nil {
		t.Fatal(err)
	}
	defer colFile.Close()
	mapping, err := (dotnet.Adapter{}).ParseTestList(colFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(mapping) != 4 {
		t.Errorf("collected %d tests, want 4 across both projects: %v", len(mapping), mapping)
	}
	if mapping["Demo.Tests.CalculatorTests.AddWorks"] != "Demo.Tests.CalculatorTests::AddWorks" {
		t.Errorf("mapping = %v", mapping)
	}
}

func testNames(doc dotnet.CoverJSON) []string {
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
