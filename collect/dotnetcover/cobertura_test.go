package dotnetcover

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// A coverlet.collector-style Cobertura report, in the shape a real
// multi-project solution emits:
// <source> root + class filenames relative to it. Two classes share Calc.cs to
// exercise per-file line merging; a zero-hit line and an out-of-repo class are
// dropped.
const coberturaSample = `<?xml version="1.0" encoding="utf-8"?>
<coverage line-rate="0.5" version="1.9">
  <sources>
    <source>/repo/root</source>
  </sources>
  <packages>
    <package name="Demo">
      <classes>
        <class name="Demo.Calculator" filename="src/Calc.cs">
          <lines>
            <line number="1" hits="1"/>
            <line number="2" hits="0"/>
            <line number="3" hits="2"/>
          </lines>
        </class>
        <class name="Demo.Calculator.Helpers" filename="src/Calc.cs">
          <lines>
            <line number="9" hits="1"/>
          </lines>
        </class>
        <class name="Demo.Greeter" filename="src/Greet.cs">
          <lines>
            <line number="10" hits="3"/>
          </lines>
        </class>
        <class name="ThirdParty.Thing" filename="/nuget/cache/Thing.cs">
          <lines>
            <line number="1" hits="5"/>
          </lines>
        </class>
      </classes>
    </package>
  </packages>
</coverage>`

func TestParseCobertura(t *testing.T) {
	p := filepath.Join(t.TempDir(), "coverage.cobertura.xml")
	if err := os.WriteFile(p, []byte(coberturaSample), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := parseCobertura(p, "/repo/root")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]int{
		"src/Calc.cs":  {1, 3, 9}, // two classes merged, zero-hit line 2 dropped
		"src/Greet.cs": {10},
		// /nuget/cache/Thing.cs is outside /repo/root and must be excluded.
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseCobertura = %v, want %v", got, want)
	}
}

func TestParseCobertura_AbsoluteFilename(t *testing.T) {
	// Some coverlet configs emit an empty source and absolute class filenames.
	const xml = `<coverage><sources><source></source></sources><packages><package>
<classes><class filename="/repo/root/src/A.cs"><lines><line number="4" hits="1"/></lines></class></classes>
</package></packages></coverage>`
	p := filepath.Join(t.TempDir(), "c.cobertura.xml")
	if err := os.WriteFile(p, []byte(xml), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := parseCobertura(p, "/repo/root")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string][]int{"src/A.cs": {4}}; !reflect.DeepEqual(got, want) {
		t.Errorf("parseCobertura = %v, want %v", got, want)
	}
}

func TestMatchFilter(t *testing.T) {
	fqn := "Contoso.Web.Test.WebModels.JsFileTests.Works"
	cases := []struct {
		filter string
		want   bool
	}{
		{"", true},
		{"FullyQualifiedName~Contoso.Web.Test.WebModels.JsFileTests", true},
		{"FullyQualifiedName~Nope", false},
		{"FullyQualifiedName=Contoso.Web.Test.WebModels.JsFileTests.Works", true},
		{"FullyQualifiedName=Contoso.Web.Test.WebModels.JsFileTests", false}, // exact, not a method
		{"JsFileTests", true}, // bare substring
		{"FullyQualifiedName~A|FullyQualifiedName~JsFileTests", true}, // OR
	}
	for _, c := range cases {
		if got := matchFilter(fqn, c.filter); got != c.want {
			t.Errorf("matchFilter(%q) = %v, want %v", c.filter, got, c.want)
		}
	}
}

func TestRun_InvalidCovMode(t *testing.T) {
	if err := run("x", ".", "o.json", "c.txt", "t.json", "lcov", "", "", false, 1); err == nil {
		t.Fatal("want error for invalid -cov-mode")
	}
}

func TestParseTrxDuration(t *testing.T) {
	cases := map[string]int64{
		"00:00:00.1234567": 123,
		"00:00:01.5000000": 1500,
		"00:01:02.0000000": 62000,
		"01:00:00.0000000": 3600000,
		"":                 0,
		"garbage":          0,
	}
	for in, want := range cases {
		if got := parseTrxDuration(in); got != want {
			t.Errorf("parseTrxDuration(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestTrxDurationMs_SumsResults(t *testing.T) {
	const trx = `<?xml version="1.0" encoding="UTF-8"?>
<TestRun xmlns="http://microsoft.com/schemas/VisualStudio/TeamTest/2010">
  <Results>
    <UnitTestResult testName="A" duration="00:00:00.2500000" outcome="Passed"/>
    <UnitTestResult testName="A" duration="00:00:00.2500000" outcome="Passed"/>
  </Results>
</TestRun>`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "results.trx"), []byte(trx), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := trxDurationMs(dir); got != 500 {
		t.Errorf("trxDurationMs = %d, want 500 (two 250ms rows summed, namespace ignored)", got)
	}
}
