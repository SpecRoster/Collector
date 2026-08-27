package cargo

import (
	"reflect"
	"strings"
	"testing"
)

const sampleCoverJSON = `{
  "format": "specroster/rust-cover/v1",
  "module": "sample",
  "tests": {
    "calc_test::test_add": {
      "src/lib.rs": [5, 6]
    },
    "calc_test::test_sub": {
      "src/lib.rs": [10, 11]
    },
    "greet_test::test_greet": {
      "src/lib.rs": [15, 16],
      "src/text.rs": [3, 4]
    }
  }
}`

func TestParseCoverage(t *testing.T) {
	cov, err := Adapter{}.ParseCoverage(strings.NewReader(sampleCoverJSON))
	if err != nil {
		t.Fatalf("ParseCoverage: %v", err)
	}
	if got := cov.LineTests["src/lib.rs"][5]; len(got) != 1 || got[0] != "calc_test::test_add" {
		t.Errorf("lib.rs line 5 = %v", got)
	}
	if got := cov.LineTests["src/text.rs"][3]; len(got) != 1 || got[0] != "greet_test::test_greet" {
		t.Errorf("text.rs line 3 = %v (cross-file coverage missing)", got)
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
	in := `calc_test::test_add
calc_test::test_sub
sample::util::tests::test_helper

some stray line without separator
`
	got, err := Adapter{}.ParseTestList(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"calc_test::test_add":              "calc_test::test_add",
		"calc_test::test_sub":              "calc_test::test_sub",
		"sample::util::tests::test_helper": "sample::util::tests::test_helper",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseTestList = %v", got)
	}
}

func TestInvocationArgs(t *testing.T) {
	got := Adapter{}.InvocationArgs([]string{
		"greet_test::test_greet",
		"calc_test::test_add",
		"calc_test::test_add", // dup
	})
	want := []string{"--", "--exact", "calc_test::test_add", "greet_test::test_greet"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("InvocationArgs = %v, want %v", got, want)
	}
	if (Adapter{}).InvocationArgs(nil) != nil {
		t.Error("empty input should produce nil args")
	}
}

func TestNormalizeJUnit(t *testing.T) {
	a := Adapter{}
	// nextest: name is the full test path.
	if got := a.NormalizeJUnit("sample", "tests::test_add"); got != "tests::test_add" {
		t.Errorf("full-path name: got %q", got)
	}
	if got := a.NormalizeJUnit("calc_test", " calc_test::test_add "); got != "calc_test::test_add" {
		t.Errorf("trim: got %q", got)
	}
	// Bare name: classname (binary/crate) supplies the root module.
	if got := a.NormalizeJUnit("calc_test", "test_add"); got != "calc_test::test_add" {
		t.Errorf("bare name: got %q", got)
	}
	if got := a.NormalizeJUnit("", "test_add"); got != "" {
		t.Errorf("empty classname: got %q", got)
	}
	if got := a.NormalizeJUnit("calc_test", ""); got != "" {
		t.Errorf("empty name: got %q", got)
	}
}

func TestNormalizeNative(t *testing.T) {
	a := Adapter{}
	if got := a.NormalizeNative("  calc_test::test_add \n"); got != "calc_test::test_add" {
		t.Errorf("got %q", got)
	}
	// No "::" means a file entry, not a native test ID.
	if got := a.NormalizeNative("tests/calc_test.rs"); got != "" {
		t.Errorf("file entry: got %q", got)
	}
	if got := a.NormalizeNative("bin:calc_test"); got != "" {
		t.Errorf("bin entry: got %q", got)
	}
	if got := a.NormalizeNative(""); got != "" {
		t.Errorf("empty: got %q", got)
	}
}

func TestEntriesForNewTestFiles(t *testing.T) {
	got := Adapter{}.EntriesForNewTestFiles([]string{
		"tests/greet_test.rs",
		"tests/calc_test.rs",
		"crates/foo/tests/calc_test.rs", // dedupes with the root one by stem
	})
	want := []string{"bin:calc_test", "bin:greet_test"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EntriesForNewTestFiles = %v, want %v (deduped bin entries)", got, want)
	}
}

func TestFileEntryCovers(t *testing.T) {
	a := Adapter{}
	if !a.FileEntryCovers("bin:calc_test", "calc_test::test_add") {
		t.Error("bin entry should cover its binary's tests")
	}
	if !a.FileEntryCovers("bin:calc_test", "calc_test::nested::test_deep") {
		t.Error("bin entry should cover nested modules of its binary")
	}
	if a.FileEntryCovers("bin:calc_test", "greet_test::test_greet") {
		t.Error("bin entry must not cover other binaries")
	}
	if a.FileEntryCovers("bin:calc_test", "calc_test_extra::test_x") {
		t.Error("stem must match the full first segment")
	}
	if a.FileEntryCovers("./internal/api", "calc_test::test_add") {
		t.Error("non-bin entries must not match")
	}
	if a.FileEntryCovers("bin:", "::test") {
		t.Error("empty stem must not match")
	}
}
