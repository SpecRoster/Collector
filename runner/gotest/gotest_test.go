package gotest

import (
	"reflect"
	"strings"
	"testing"
)

const sampleCoverJSON = `{
  "format": "specroster/go-cover/v1",
  "module": "example.com/sample",
  "tests": {
    "example.com/sample/calc.TestAdd": {
      "calc/calc.go": [5, 6]
    },
    "example.com/sample/calc.TestSub": {
      "calc/calc.go": [10, 11]
    },
    "example.com/sample/strs.TestUpper": {
      "strs/strs.go": [7],
      "calc/calc.go": [5]
    }
  }
}`

func TestParseCoverage(t *testing.T) {
	cov, err := Adapter{}.ParseCoverage(strings.NewReader(sampleCoverJSON))
	if err != nil {
		t.Fatalf("ParseCoverage: %v", err)
	}
	line5 := cov.LineTests["calc/calc.go"][5]
	if len(line5) != 2 {
		t.Errorf("calc.go line 5 tests = %v, want TestAdd + TestUpper (cross-package)", line5)
	}
	if got := cov.LineTests["strs/strs.go"][7]; len(got) != 1 || got[0] != "example.com/sample/strs.TestUpper" {
		t.Errorf("strs.go line 7 = %v", got)
	}
}

func TestParseCoverageRejectsWrongFormat(t *testing.T) {
	if _, err := (Adapter{}).ParseCoverage(strings.NewReader(`{"format":"something-else","tests":{}}`)); err == nil {
		t.Error("wrong format accepted")
	}
	if _, err := (Adapter{}).ParseCoverage(strings.NewReader(`not json`)); err == nil {
		t.Error("garbage accepted")
	}
}

func TestParseTestList(t *testing.T) {
	in := `example.com/sample/calc::TestAdd
example.com/sample/calc::TestSub
example.com/sample/strs::TestUpper

some stray line
`
	got, err := Adapter{}.ParseTestList(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"example.com/sample/calc.TestAdd":   "example.com/sample/calc::TestAdd",
		"example.com/sample/calc.TestSub":   "example.com/sample/calc::TestSub",
		"example.com/sample/strs.TestUpper": "example.com/sample/strs::TestUpper",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseTestList = %v", got)
	}
}

func TestInvocationArgs(t *testing.T) {
	got := Adapter{}.InvocationArgs([]string{
		"example.com/sample/calc::TestAdd",
		"example.com/sample/strs::TestUpper/sub_case",
		"example.com/sample/calc::TestAdd", // dup
	})
	want := []string{"-run", "^(TestAdd|TestUpper)$",
		"example.com/sample/calc", "example.com/sample/strs"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("InvocationArgs = %v, want %v", got, want)
	}
	if (Adapter{}).InvocationArgs(nil) != nil {
		t.Error("empty input should produce nil args")
	}
}

func TestNormalizeJUnit(t *testing.T) {
	a := Adapter{}
	if got := a.NormalizeJUnit("example.com/sample/calc", "TestAdd"); got != "example.com/sample/calc.TestAdd" {
		t.Errorf("got %q", got)
	}
	// Subtests collapse onto the parent.
	if got := a.NormalizeJUnit("example.com/sample/calc", "TestAdd/negative_numbers"); got != "example.com/sample/calc.TestAdd" {
		t.Errorf("subtest: got %q", got)
	}
	if got := a.NormalizeJUnit("", "TestAdd"); got != "" {
		t.Errorf("empty classname: got %q", got)
	}
}

func TestEntriesForNewTestFiles(t *testing.T) {
	got := Adapter{}.EntriesForNewTestFiles([]string{
		"internal/api/new_test.go",
		"internal/api/other_test.go",
		"cmd/app/main_test.go",
	})
	want := []string{"./cmd/app", "./internal/api"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EntriesForNewTestFiles = %v, want %v (deduped package dirs)", got, want)
	}
}

func TestFileEntryCovers(t *testing.T) {
	a := Adapter{}
	if !a.FileEntryCovers("./internal/api", "github.com/x/y/internal/api.TestFoo") {
		t.Error("package entry should cover its tests")
	}
	if a.FileEntryCovers("./internal/api", "github.com/x/y/internal/store.TestBar") {
		t.Error("package entry must not cover other packages")
	}
	if a.FileEntryCovers("tests/test_x.py", "anything") {
		t.Error("non-package entries must not match")
	}
}
