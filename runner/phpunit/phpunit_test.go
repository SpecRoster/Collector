package phpunit

import (
	"reflect"
	"strings"
	"testing"
)

const sampleCoverJSON = `{
  "format": "specroster/php-cover/v1",
  "tests": {
    "Demo.Tests.CalculatorTest.testAdd": {"src/Calculator.php": [11, 12]},
    "Demo.Tests.CalculatorTest.testSub": {"src/Calculator.php": [16]},
    "Demo.Tests.GreeterTest.testGreet": {"src/Greeter.php": [11], "src/Calculator.php": [11]}
  }
}`

func TestParseCoverage(t *testing.T) {
	cov, err := Adapter{}.ParseCoverage(strings.NewReader(sampleCoverJSON))
	if err != nil {
		t.Fatalf("ParseCoverage: %v", err)
	}
	if got := cov.LineTests["src/Calculator.php"][11]; len(got) != 2 {
		t.Errorf("Calculator.php:11 = %v, want testAdd + testGreet (cross-file)", got)
	}
	if _, err := (Adapter{}).ParseCoverage(strings.NewReader(`{"format":"other","tests":{}}`)); err == nil {
		t.Error("wrong format accepted")
	}
}

func TestParseTestListAndNormalize(t *testing.T) {
	in := `Demo\Tests\CalculatorTest::testAdd
Demo\Tests\CalculatorTest::testMany with data set #0
Demo\Tests\CalculatorTest::testMany with data set "two"
stray
`
	got, err := Adapter{}.ParseTestList(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Demo.Tests.CalculatorTest.testAdd":  `Demo\Tests\CalculatorTest::testAdd`,
		"Demo.Tests.CalculatorTest.testMany": `Demo\Tests\CalculatorTest::testMany with data set #0`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseTestList = %v", got)
	}
}

func TestNormalizeNative(t *testing.T) {
	a := Adapter{}
	cases := []struct{ in, want string }{
		{`Demo\Tests\CalculatorTest::testAdd`, "Demo.Tests.CalculatorTest.testAdd"},
		{`Demo\Tests\CalculatorTest::testMany with data set #0`, "Demo.Tests.CalculatorTest.testMany"},
		{`Demo\Tests\CalculatorTest::testMany with data set "two"`, "Demo.Tests.CalculatorTest.testMany"},
		{`Demo\Tests\CalculatorTest::testMany#3`, "Demo.Tests.CalculatorTest.testMany"},
		{"stray", ""},
		{"::testAdd", ""},
	}
	for _, c := range cases {
		if got := a.NormalizeNative(c.in); got != c.want {
			t.Errorf("NormalizeNative(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeJUnit(t *testing.T) {
	a := Adapter{}
	cases := []struct{ class, name, want string }{
		{`Demo\Tests\CalculatorTest`, "testAdd", "Demo.Tests.CalculatorTest.testAdd"},
		{`Demo\Tests\CalculatorTest`, `testMany with data set "two"`, "Demo.Tests.CalculatorTest.testMany"},
		{`Demo\Tests\CalculatorTest`, "testMany#2", "Demo.Tests.CalculatorTest.testMany"},
		// PHPUnit's JUnit XML also carries a dot-separated classname attr.
		{"Demo.Tests.CalculatorTest", "testAdd", "Demo.Tests.CalculatorTest.testAdd"},
		// Some emitters put the native ID in name already.
		{`Demo\Tests\CalculatorTest`, `Demo\Tests\CalculatorTest::testAdd`, "Demo.Tests.CalculatorTest.testAdd"},
		{"", "testAdd", ""},
		{`Demo\Tests\CalculatorTest`, "", ""},
	}
	for _, c := range cases {
		if got := a.NormalizeJUnit(c.class, c.name); got != c.want {
			t.Errorf("NormalizeJUnit(%q,%q) = %q, want %q", c.class, c.name, got, c.want)
		}
	}
}

func TestInvocationArgs(t *testing.T) {
	got := Adapter{}.InvocationArgs([]string{
		`Demo\Tests\GreeterTest::testGreet`,
		`Demo\Tests\CalculatorTest::testAdd`,
		`Demo\Tests\CalculatorTest::testAdd with data set #0`, // dup after stripping
	})
	want := []string{"--filter",
		`^(?:Demo\\Tests\\CalculatorTest::testAdd|Demo\\Tests\\GreeterTest::testGreet)$`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("InvocationArgs = %q, want %q", got, want)
	}
	// Backslash escaping, exactly: each PHP namespace separator must be
	// TWO backslash characters in the final argument string.
	if !strings.Contains(got[1], "Demo\\\\Tests\\\\CalculatorTest::testAdd") {
		t.Errorf("filter regex %q does not regex-escape namespace backslashes", got[1])
	}
	if strings.Contains(got[1], "\\\\\\") {
		t.Errorf("filter regex %q over-escapes backslashes", got[1])
	}
	if (Adapter{}).InvocationArgs(nil) != nil {
		t.Error("InvocationArgs(nil) should be nil")
	}
}

func TestNewTestFileEntries(t *testing.T) {
	a := Adapter{}
	entries := a.EntriesForNewTestFiles([]string{"tests/ReceiptTest.php"})
	if !reflect.DeepEqual(entries, []string{"class:ReceiptTest"}) {
		t.Fatalf("entries = %v", entries)
	}
	if !a.FileEntryCovers("class:ReceiptTest", "Demo.Tests.ReceiptTest.testTotals") {
		t.Error("class entry should cover its tests")
	}
	if a.FileEntryCovers("class:ReceiptTest", "Demo.Tests.OtherTest.testX") {
		t.Error("class entry must not cover other classes")
	}
}
