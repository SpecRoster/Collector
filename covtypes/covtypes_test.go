package covtypes

import "testing"

func TestUnderDirIsSegmentAware(t *testing.T) {
	if underDir("richer/x.py", "rich") {
		t.Error("richer/ matched rich/ prefix")
	}
	if !underDir("rich/sub/x.py", "rich") {
		t.Error("rich/sub not matched")
	}
}

func TestDirLayoutClassifyFile(t *testing.T) {
	l := DirLayout{SrcDir: "rich", TestDir: "tests", Suffix: ".py"}
	cases := []struct {
		path string
		want FileKind
	}{
		{"rich/console.py", KindSource},
		{"tests/test_console.py", KindTest},
		{"README.md", KindOther},
		{"richer/console.py", KindOther},
	}
	for _, c := range cases {
		if got := l.ClassifyFile(c.path); got != c.want {
			t.Errorf("ClassifyFile(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestTestsInTestFileMatchesByBasename(t *testing.T) {
	l := DirLayout{SrcDir: "rich", TestDir: "tests", Suffix: ".py"}
	all := TestSet{}
	all.Add("test_console.test_print")
	all.Add("test_console.test_wrap")
	all.Add("test_style.test_parse")

	got := l.TestsInTestFile("tests/test_console.py", all).Sorted()
	want := []string{"test_console.test_print", "test_console.test_wrap"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
			break
		}
	}
}
