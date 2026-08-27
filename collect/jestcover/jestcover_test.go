package jestcover

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/SpecRoster/Collector/runner/jest"
)

// TestCollectorEndToEnd runs the collector against the fixture project in
// testdata/sample (greet.js calls into calc.js so cross-file coverage is
// observable). Requires node/npm; the first run does an npm install
// (network).
func TestCollectorEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("node/npx not on PATH")
	}
	if testing.Short() {
		t.Skip("short mode")
	}
	if _, err := os.Stat("testdata/sample/node_modules/.bin/jest"); err != nil {
		install := exec.Command("npm", "install", "--no-audit", "--no-fund")
		install.Dir = "testdata/sample"
		if out, err := install.CombinedOutput(); err != nil {
			t.Fatalf("npm install: %v\n%s", err, out)
		}
	}

	tmp := t.TempDir()
	covPath := filepath.Join(tmp, "coverage.json")
	colPath := filepath.Join(tmp, "collected.txt")
	if err := run("testdata/sample", "", covPath, colPath); err != nil {
		t.Fatalf("run: %v", err)
	}

	raw, err := os.ReadFile(covPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc jest.CoverJSON
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Format != jest.CoverageFormat {
		t.Errorf("format = %q", doc.Format)
	}

	calcCov := doc.Tests["__tests__/calc.test.js"]
	if calcCov == nil {
		t.Fatalf("no coverage for calc spec; have %v", names(doc))
	}
	if len(calcCov["src/calc.js"]) == 0 {
		t.Error("calc spec does not cover src/calc.js")
	}
	if _, ok := calcCov["src/greet.js"]; ok {
		t.Error("calc spec must not cover src/greet.js")
	}

	greetCov := doc.Tests["__tests__/greet.test.js"]
	if greetCov == nil {
		t.Fatalf("no coverage for greet spec; have %v", names(doc))
	}
	if len(greetCov["src/greet.js"]) == 0 {
		t.Error("greet spec does not cover its own module")
	}
	if len(greetCov["src/calc.js"]) == 0 {
		t.Error("greet spec does not record cross-file coverage of calc.js")
	}
	// Per-spec isolation: greet must not cover calc.js's sub().
	if len(greetCov["src/calc.js"]) >= len(calcCov["src/calc.js"]) {
		t.Errorf("greet covers %d calc.js lines vs calc spec's %d — isolation looks broken",
			len(greetCov["src/calc.js"]), len(calcCov["src/calc.js"]))
	}

	colFile, err := os.Open(colPath)
	if err != nil {
		t.Fatal(err)
	}
	defer colFile.Close()
	mapping, err := (jest.Adapter{}).ParseTestList(colFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(mapping) != 2 {
		t.Errorf("collected %d specs, want 2: %v", len(mapping), mapping)
	}
}

func names(doc jest.CoverJSON) []string {
	out := make([]string, 0, len(doc.Tests))
	for k := range doc.Tests {
		out = append(out, k)
	}
	return out
}
