package rbcover

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/SpecRoster/Collector/runner/rspec"
)

// TestCollectorEndToEnd runs the collector against the fixture project in
// testdata/sample (greet.rb calls into calc.rb so cross-file coverage is
// observable). Requires ruby/bundler; the first run does a bundle install
// (network), kept local to the fixture via BUNDLE_PATH.
func TestCollectorEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("bundle"); err != nil {
		t.Skip("ruby/bundler not on PATH")
	}
	if testing.Short() {
		t.Skip("short mode")
	}
	// Relative BUNDLE_PATH resolves against the Gemfile's directory; the
	// collector's child processes inherit it from this test's environment.
	t.Setenv("BUNDLE_PATH", "vendor/bundle")
	check := exec.Command("bundle", "check")
	check.Dir = "testdata/sample"
	if err := check.Run(); err != nil {
		install := exec.Command("bundle", "install")
		install.Dir = "testdata/sample"
		if out, err := install.CombinedOutput(); err != nil {
			t.Fatalf("bundle install: %v\n%s", err, out)
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
	var doc rspec.CoverJSON
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Format != rspec.CoverageFormat {
		t.Errorf("format = %q", doc.Format)
	}

	calcCov := doc.Tests["spec/calc_spec.rb"]
	if calcCov == nil {
		t.Fatalf("no coverage for calc spec; have %v", names(doc))
	}
	if len(calcCov["lib/calc.rb"]) == 0 {
		t.Error("calc spec does not cover lib/calc.rb")
	}
	if _, ok := calcCov["lib/greet.rb"]; ok {
		t.Error("calc spec must not cover lib/greet.rb")
	}

	greetCov := doc.Tests["spec/greet_spec.rb"]
	if greetCov == nil {
		t.Fatalf("no coverage for greet spec; have %v", names(doc))
	}
	if len(greetCov["lib/greet.rb"]) == 0 {
		t.Error("greet spec does not cover its own module")
	}
	if len(greetCov["lib/calc.rb"]) == 0 {
		t.Error("greet spec does not record cross-file coverage of calc.rb")
	}
	// Per-spec isolation: greet must not cover calc.rb's sub() body.
	if len(greetCov["lib/calc.rb"]) >= len(calcCov["lib/calc.rb"]) {
		t.Errorf("greet covers %d calc.rb lines vs calc spec's %d — isolation looks broken",
			len(greetCov["lib/calc.rb"]), len(calcCov["lib/calc.rb"]))
	}

	colFile, err := os.Open(colPath)
	if err != nil {
		t.Fatal(err)
	}
	defer colFile.Close()
	mapping, err := (rspec.Adapter{}).ParseTestList(colFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(mapping) != 2 {
		t.Errorf("collected %d specs, want 2: %v", len(mapping), mapping)
	}
}

// TestDecodeLinesBothShapes covers the two .resultset.json file-value shapes
// SimpleCov has emitted over time.
func TestDecodeLinesBothShapes(t *testing.T) {
	modern, err := decodeLines(json.RawMessage(`{"lines": [null, 1, 0, 2]}`))
	if err != nil {
		t.Fatalf("modern shape: %v", err)
	}
	old, err := decodeLines(json.RawMessage(`[null, 1, 0, 2]`))
	if err != nil {
		t.Fatalf("old shape: %v", err)
	}
	for name, hits := range map[string][]*float64{"modern": modern, "old": old} {
		if len(hits) != 4 || hits[0] != nil || *hits[1] != 1 || *hits[2] != 0 || *hits[3] != 2 {
			t.Errorf("%s shape decoded wrong: %v", name, hits)
		}
	}
	if _, err := decodeLines(json.RawMessage(`"bogus"`)); err == nil {
		t.Error("bogus shape accepted")
	}
}

// TestParseResultsetShapes verifies line numbering (index i → line i+1),
// covered = hits > 0, null = non-executable, and source-only filtering, for
// both resultset shapes.
func TestParseResultsetShapes(t *testing.T) {
	root := t.TempDir()
	resultset := `{
	  "RSpec": {
	    "coverage": {
	      "` + filepath.ToSlash(filepath.Join(root, "lib/calc.rb")) + `": {"lines": [1, 1, null, 0, 3]},
	      "` + filepath.ToSlash(filepath.Join(root, "lib/old.rb")) + `": [null, 2, 0],
	      "` + filepath.ToSlash(filepath.Join(root, "spec/calc_spec.rb")) + `": {"lines": [1]},
	      "/outside/elsewhere.rb": {"lines": [1]}
	    }
	  }
	}`
	path := filepath.Join(root, ".resultset.json")
	if err := os.WriteFile(path, []byte(resultset), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := parseResultset(path, root, rspec.Adapter{}.Layout("", ""))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]int{
		"lib/calc.rb": {1, 2, 5},
		"lib/old.rb":  {2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseResultset = %v, want %v", got, want)
	}
}

func names(doc rspec.CoverJSON) []string {
	out := make([]string, 0, len(doc.Tests))
	for k := range doc.Tests {
		out = append(out, k)
	}
	return out
}
