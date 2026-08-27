package dotnet

import (
	"reflect"
	"strings"
	"testing"
)

const sampleCoverJSON = `{
  "format": "specroster/dotnet-cover/v1",
  "tests": {
    "Demo.Tests.CalculatorTests.AddWorks": {"Demo/Calculator.cs": [7, 8]},
    "Demo.Tests.CalculatorTests.SubWorks": {"Demo/Calculator.cs": [12]},
    "Demo.Tests.GreeterTests.GreetsByCount": {"Demo/Greeter.cs": [9], "Demo/Calculator.cs": [7]}
  }
}`

func TestParseCoverage(t *testing.T) {
	cov, err := Adapter{}.ParseCoverage(strings.NewReader(sampleCoverJSON))
	if err != nil {
		t.Fatalf("ParseCoverage: %v", err)
	}
	if got := cov.LineTests["Demo/Calculator.cs"][7]; len(got) != 2 {
		t.Errorf("Calculator.cs:7 = %v, want AddWorks + GreetsByCount (cross-file)", got)
	}
	if _, err := (Adapter{}).ParseCoverage(strings.NewReader(`{"format":"other","tests":{}}`)); err == nil {
		t.Error("wrong format accepted")
	}
}

func TestParseTestListAndNormalize(t *testing.T) {
	in := "Demo.Tests.CalculatorTests::AddWorks\nDemo.Tests.CalculatorTests::Theory(x: 1)\nstray\n"
	got, err := Adapter{}.ParseTestList(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Demo.Tests.CalculatorTests.AddWorks": "Demo.Tests.CalculatorTests::AddWorks",
		"Demo.Tests.CalculatorTests.Theory":   "Demo.Tests.CalculatorTests::Theory(x: 1)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseTestList = %v", got)
	}
}

func TestNormalizeJUnit(t *testing.T) {
	a := Adapter{}
	cases := []struct{ class, name, want string }{
		{"Demo.Tests.CalculatorTests", "AddWorks", "Demo.Tests.CalculatorTests.AddWorks"},
		{"Demo.Tests.CalcTests", "Halves(x: 4)", "Demo.Tests.CalcTests.Halves"},
		// Some loggers put the FQN in name already.
		{"Demo.Tests.CalcTests", "Demo.Tests.CalcTests.AddWorks", "Demo.Tests.CalcTests.AddWorks"},
		{"", "AddWorks", ""},
	}
	for _, c := range cases {
		if got := a.NormalizeJUnit(c.class, c.name); got != c.want {
			t.Errorf("NormalizeJUnit(%q,%q) = %q, want %q", c.class, c.name, got, c.want)
		}
	}
}

func TestInvocationArgs(t *testing.T) {
	got := Adapter{}.InvocationArgs([]string{
		"Demo.Tests.CalculatorTests::AddWorks",
		"Demo.Tests.GreeterTests::GreetsByCount",
		"Demo.Tests.CalculatorTests::AddWorks", // dup
	})
	want := []string{"--filter",
		"FullyQualifiedName~Demo.Tests.CalculatorTests.AddWorks|FullyQualifiedName~Demo.Tests.GreeterTests.GreetsByCount"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("InvocationArgs = %v", got)
	}
}

func TestNewTestFileEntries(t *testing.T) {
	a := Adapter{}
	entries := a.EntriesForNewTestFiles([]string{"Demo.Tests/ReceiptTests.cs"})
	if !reflect.DeepEqual(entries, []string{"class:ReceiptTests"}) {
		t.Fatalf("entries = %v", entries)
	}
	if !a.FileEntryCovers("class:ReceiptTests", "Demo.Tests.ReceiptTests.TotalsWork") {
		t.Error("class entry should cover its tests")
	}
	if a.FileEntryCovers("class:ReceiptTests", "Demo.Tests.OtherTests.X") {
		t.Error("class entry must not cover other classes")
	}
}
