package jest

import (
	"reflect"
	"strings"
	"testing"
)

const sampleCoverJSON = `{
  "format": "specroster/jest-cover/v1",
  "tests": {
    "__tests__/calc.test.js": {"src/calc.js": [3, 4, 7]},
    "__tests__/greet.test.js": {"src/greet.js": [5], "src/calc.js": [3]}
  }
}`

func TestParseCoverage(t *testing.T) {
	cov, err := Adapter{}.ParseCoverage(strings.NewReader(sampleCoverJSON))
	if err != nil {
		t.Fatalf("ParseCoverage: %v", err)
	}
	if got := cov.LineTests["src/calc.js"][3]; len(got) != 2 {
		t.Errorf("calc.js:3 = %v, want both spec files (cross-file)", got)
	}
	if _, err := (Adapter{}).ParseCoverage(strings.NewReader(`{"format":"nope","tests":{}}`)); err == nil {
		t.Error("wrong format accepted")
	}
}

func TestParseTestListAndNormalize(t *testing.T) {
	got, err := Adapter{}.ParseTestList(strings.NewReader("./__tests__/calc.test.js\n__tests__\\greet.test.js\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"__tests__/calc.test.js":  "__tests__/calc.test.js",
		"__tests__/greet.test.js": "__tests__/greet.test.js",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseTestList = %v", got)
	}
	if got := (Adapter{}).NormalizeJUnit("./__tests__/calc.test.js", "adds two numbers"); got != "__tests__/calc.test.js" {
		t.Errorf("NormalizeJUnit = %q (cases must collapse onto the file)", got)
	}
}

func TestNewSpecEntriesAndCoverage(t *testing.T) {
	a := Adapter{}
	entries := a.EntriesForNewTestFiles([]string{"./__tests__/new.test.js"})
	if !reflect.DeepEqual(entries, []string{"__tests__/new.test.js"}) {
		t.Fatalf("entries = %v", entries)
	}
	if !a.FileEntryCovers("__tests__/new.test.js", "./__tests__/new.test.js") {
		t.Error("entry should cover its own spec")
	}
	if a.FileEntryCovers("__tests__/new.test.js", "__tests__/other.test.js") {
		t.Error("entry must not cover other specs")
	}
}
