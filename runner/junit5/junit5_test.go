package junit5

import (
	"reflect"
	"strings"
	"testing"
)

const sampleCoverJSON = `{
  "format": "specroster/jvm-cover/v1",
  "tests": {
    "demo.CalculatorTest.testAdd": {"src/main/java/demo/Calculator.java": [7, 8]},
    "demo.CalculatorTest.testSub": {"src/main/java/demo/Calculator.java": [12]},
    "demo.GreeterTest.testGreet": {"src/main/java/demo/Greeter.java": [9], "src/main/java/demo/Calculator.java": [7]}
  }
}`

func TestParseCoverage(t *testing.T) {
	cov, err := Adapter{}.ParseCoverage(strings.NewReader(sampleCoverJSON))
	if err != nil {
		t.Fatalf("ParseCoverage: %v", err)
	}
	if got := cov.LineTests["src/main/java/demo/Calculator.java"][7]; len(got) != 2 {
		t.Errorf("Calculator.java:7 = %v, want testAdd + testGreet (cross-file)", got)
	}
	if _, err := (Adapter{}).ParseCoverage(strings.NewReader(`{"format":"other","tests":{}}`)); err == nil {
		t.Error("wrong format accepted")
	}
}

func TestParseTestListAndNormalize(t *testing.T) {
	in := "demo.CalculatorTest::testAdd\ndemo.CalculatorTest::testParam(int)[1]\nstray\n"
	got, err := Adapter{}.ParseTestList(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"demo.CalculatorTest.testAdd":   "demo.CalculatorTest::testAdd",
		"demo.CalculatorTest.testParam": "demo.CalculatorTest::testParam(int)[1]",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseTestList = %v", got)
	}
}

func TestNormalizeJUnit(t *testing.T) {
	a := Adapter{}
	cases := []struct{ class, name, want string }{
		{"demo.CalculatorTest", "testAdd", "demo.CalculatorTest.testAdd"},
		// Surefire renders plain methods with a "()" suffix.
		{"demo.CalculatorTest", "testAdd()", "demo.CalculatorTest.testAdd"},
		// Parametrized invocations carry "[n]" (with or without an arg list).
		{"demo.CalculatorTest", "testParam(int)[1]", "demo.CalculatorTest.testParam"},
		{"demo.CalculatorTest", "testParam[2]", "demo.CalculatorTest.testParam"},
		// @Nested classes keep their binary "$Nested" classname.
		{"demo.CalculatorTest$Edge", "testOverflow()", "demo.CalculatorTest$Edge.testOverflow"},
		{"", "testAdd", ""},
		{"demo.CalculatorTest", "", ""},
	}
	for _, c := range cases {
		if got := a.NormalizeJUnit(c.class, c.name); got != c.want {
			t.Errorf("NormalizeJUnit(%q,%q) = %q, want %q", c.class, c.name, got, c.want)
		}
	}
}

func TestNormalizeNative(t *testing.T) {
	a := Adapter{}
	cases := []struct{ in, want string }{
		{"demo.CalculatorTest::testAdd", "demo.CalculatorTest.testAdd"},
		{"demo.CalculatorTest::testParam(int)[1]", "demo.CalculatorTest.testParam"},
		{"demo.CalculatorTest$Edge::testOverflow", "demo.CalculatorTest$Edge.testOverflow"},
		{"no-separator", ""},
		{"::testAdd", ""},
	}
	for _, c := range cases {
		if got := a.NormalizeNative(c.in); got != c.want {
			t.Errorf("NormalizeNative(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestInvocationArgs(t *testing.T) {
	got := Adapter{}.InvocationArgs([]string{
		"demo.GreeterTest::testGreet",
		"demo.CalculatorTest::testSub",
		"demo.CalculatorTest::testAdd",
		"demo.CalculatorTest::testAdd", // dup
	})
	want := []string{"-Dtest=demo.CalculatorTest#testAdd+testSub,demo.GreeterTest#testGreet"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("InvocationArgs = %v, want %v", got, want)
	}
	if got := (Adapter{}).InvocationArgs(nil); got != nil {
		t.Errorf("InvocationArgs(nil) = %v, want nil", got)
	}
}

func TestNewTestFileEntries(t *testing.T) {
	a := Adapter{}
	entries := a.EntriesForNewTestFiles([]string{"src/test/java/demo/ReceiptTest.java"})
	if !reflect.DeepEqual(entries, []string{"class:ReceiptTest"}) {
		t.Fatalf("entries = %v", entries)
	}
	if !a.FileEntryCovers("class:ReceiptTest", "demo.ReceiptTest.totalsWork") {
		t.Error("class entry should cover its tests")
	}
	if !a.FileEntryCovers("class:ReceiptTest", "demo.ReceiptTest$Edge.handlesZero") {
		t.Error("class entry should cover its @Nested classes' tests")
	}
	if a.FileEntryCovers("class:ReceiptTest", "demo.OtherTest.x") {
		t.Error("class entry must not cover other classes")
	}
	if a.FileEntryCovers("class:ReceiptTest", "demo.ReceiptTestExtra.x") {
		t.Error("class entry must not prefix-match unrelated class names")
	}
}
