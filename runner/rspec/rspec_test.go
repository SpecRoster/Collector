package rspec

import (
	"reflect"
	"strings"
	"testing"
)

const sampleCoverJSON = `{
  "format": "specroster/rspec-cover/v1",
  "tests": {
    "spec/calc_spec.rb": {"lib/calc.rb": [2, 3, 6]},
    "spec/greet_spec.rb": {"lib/greet.rb": [4], "lib/calc.rb": [2]}
  }
}`

func TestParseCoverage(t *testing.T) {
	cov, err := Adapter{}.ParseCoverage(strings.NewReader(sampleCoverJSON))
	if err != nil {
		t.Fatalf("ParseCoverage: %v", err)
	}
	if got := cov.LineTests["lib/calc.rb"][2]; len(got) != 2 {
		t.Errorf("calc.rb:2 = %v, want both spec files (cross-file)", got)
	}
	if _, err := (Adapter{}).ParseCoverage(strings.NewReader(`{"format":"nope","tests":{}}`)); err == nil {
		t.Error("wrong format accepted")
	}
}

func TestParseTestListAndNormalize(t *testing.T) {
	got, err := Adapter{}.ParseTestList(strings.NewReader("./spec/calc_spec.rb\nspec\\greet_spec.rb\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"spec/calc_spec.rb":  "spec/calc_spec.rb",
		"spec/greet_spec.rb": "spec/greet_spec.rb",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseTestList = %v", got)
	}
}

// TestNormalizeJUnit covers the rspec_junit_formatter round-trip: the
// formatter emits classnames as dotted paths with ".rb" stripped, which must
// reconstruct to the canonical spec path.
func TestNormalizeJUnit(t *testing.T) {
	a := Adapter{}
	cases := map[string]string{
		"spec.models.user_spec": "spec/models/user_spec.rb", // dotted form
		"spec.calc_spec":        "spec/calc_spec.rb",
		"spec/calc_spec.rb":     "spec/calc_spec.rb", // already a path
		"./spec/calc_spec.rb":   "spec/calc_spec.rb",
		"calc_spec.rb":          "calc_spec.rb", // path form, repo root
	}
	for classname, want := range cases {
		if got := a.NormalizeJUnit(classname, "adds two numbers"); got != want {
			t.Errorf("NormalizeJUnit(%q) = %q, want %q (cases must collapse onto the file)", classname, got, want)
		}
	}
}

func TestNewSpecEntriesAndCoverage(t *testing.T) {
	a := Adapter{}
	entries := a.EntriesForNewTestFiles([]string{"./spec/new_spec.rb"})
	if !reflect.DeepEqual(entries, []string{"spec/new_spec.rb"}) {
		t.Fatalf("entries = %v", entries)
	}
	if !a.FileEntryCovers("spec/new_spec.rb", "./spec/new_spec.rb") {
		t.Error("entry should cover its own spec")
	}
	if a.FileEntryCovers("spec/new_spec.rb", "spec/other_spec.rb") {
		t.Error("entry must not cover other specs")
	}
}
