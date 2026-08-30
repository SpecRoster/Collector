package dotnetcover

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if err := run("", "testdata/sample", covPath, colPath, timPath, "msbuild", "", "", false, 2); err != nil {
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

// TestOnlyCollectsSubsetButReportsFullInventory pins the contract incremental
// snapshots depend on: collection is expensive so it is restricted, but
// LISTING is cheap so the inventory stays complete. The server uses that
// inventory to tell "not re-collected, carry it forward" from "deleted, drop
// it" — if the inventory shrank to the subset, every test outside it would
// look deleted and be erased from the snapshot.
func TestOnlyCollectsSubsetButReportsFullInventory(t *testing.T) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet SDK not on PATH")
	}
	if testing.Short() {
		t.Skip("short mode")
	}
	tmp := t.TempDir()
	covPath := filepath.Join(tmp, "coverage.json")
	colPath := filepath.Join(tmp, "collected.txt")
	onlyPath := filepath.Join(tmp, "only.txt")

	const target = "Demo.Tests.CalculatorTests.AddWorks"
	if err := os.WriteFile(onlyPath, []byte(target+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run("", "testdata/sample", covPath, colPath, "", "msbuild", "", onlyPath, false, 2); err != nil {
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
	if len(doc.Tests) != 1 {
		t.Errorf("collected coverage for %d tests, want 1: %v", len(doc.Tests), testNames(doc))
	}
	if doc.Tests[target] == nil {
		t.Errorf("no coverage for the requested test %q; have %v", target, testNames(doc))
	}

	inventory, err := os.ReadFile(colPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for _, l := range strings.Split(strings.TrimSpace(string(inventory)), "\n") {
		if strings.TrimSpace(l) != "" {
			lines++
		}
	}
	if lines <= 1 {
		t.Errorf("inventory has %d entries, want the whole suite — a subset run must still report every test that exists", lines)
	}
}

// TestListOnlyProducesInventoryWithoutCollecting covers the cheap half of the
// asymmetry incremental snapshots rest on. The server cannot plan which slice
// of a suite went stale until it knows the whole suite, and paying a full
// collection to find that out would defeat the entire feature.
func TestListOnlyProducesInventoryWithoutCollecting(t *testing.T) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet SDK not on PATH")
	}
	if testing.Short() {
		t.Skip("short mode")
	}
	tmp := t.TempDir()
	covPath := filepath.Join(tmp, "coverage.json")
	colPath := filepath.Join(tmp, "collected.txt")

	start := time.Now()
	if err := run("", "testdata/sample", covPath, colPath, "", "msbuild", "", "", true, 2); err != nil {
		t.Fatalf("run: %v", err)
	}
	listing := time.Since(start)

	raw, err := os.ReadFile(colPath)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(l) != "" {
			names = append(names, l)
		}
	}
	if len(names) < 2 {
		t.Errorf("inventory has %d entries, want the whole sample suite: %v", len(names), names)
	}
	if _, err := os.Stat(covPath); err == nil {
		t.Error("a list-only pass wrote a coverage file; it must not pretend to have collected anything")
	}
	t.Logf("listed %d tests in %s without collecting", len(names), listing.Round(time.Millisecond))
}
