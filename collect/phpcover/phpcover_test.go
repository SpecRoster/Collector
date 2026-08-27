package phpcover

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SpecRoster/Collector/runner/phpunit"
)

// TestCollectorEndToEnd runs the collector against the fixture project in
// testdata/sample (Demo\Calculator + Demo\Greeter; Greeter calls into
// Calculator so cross-file coverage is observable). Requires php and
// composer plus a coverage driver (Xdebug or PCOV); skipped where absent.
// Composer install needs network on first run.
func TestCollectorEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("php not on PATH")
	}
	if _, err := exec.LookPath("composer"); err != nil {
		t.Skip("composer not on PATH")
	}
	if testing.Short() {
		t.Skip("short mode")
	}
	if mods, err := exec.Command("php", "-m").Output(); err == nil {
		lower := strings.ToLower(string(mods))
		if !strings.Contains(lower, "xdebug") && !strings.Contains(lower, "pcov") {
			t.Skip("no PHP coverage driver (xdebug or pcov) installed")
		}
	}
	if _, err := os.Stat(filepath.Join("testdata", "sample", "vendor")); os.IsNotExist(err) {
		install := exec.Command("composer", "install", "--no-interaction")
		install.Dir = filepath.Join("testdata", "sample")
		if out, err := install.CombinedOutput(); err != nil {
			t.Skipf("composer install failed (php version or network?): %v\n%s", err, out)
		}
	}

	tmp := t.TempDir()
	covPath := filepath.Join(tmp, "coverage.json")
	colPath := filepath.Join(tmp, "collected.txt")

	if err := run("testdata/sample", "testdata/sample", "vendor/bin/phpunit", covPath, colPath); err != nil {
		t.Fatalf("run: %v", err)
	}

	raw, err := os.ReadFile(covPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc phpunit.CoverJSON
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Format != phpunit.CoverageFormat {
		t.Errorf("format = %q", doc.Format)
	}

	addCov := doc.Tests["Demo.Tests.CalculatorTest.testAdd"]
	if addCov == nil {
		t.Fatalf("no coverage for testAdd; have %v", testNames(doc))
	}
	if len(addCov["src/Calculator.php"]) == 0 {
		t.Error("testAdd does not cover src/Calculator.php")
	}
	if _, ok := addCov["src/Greeter.php"]; ok {
		t.Error("testAdd must not cover Greeter.php")
	}

	greetCov := doc.Tests["Demo.Tests.GreeterTest.testGreet"]
	if greetCov == nil {
		t.Fatalf("no coverage for testGreet; have %v", testNames(doc))
	}
	if len(greetCov["src/Greeter.php"]) == 0 {
		t.Error("testGreet does not cover its own file")
	}
	if len(greetCov["src/Calculator.php"]) == 0 {
		t.Error("testGreet does not record cross-file coverage of Calculator.php")
	}

	// Per-test isolation: add and sub cover different lines of the file.
	subCov := doc.Tests["Demo.Tests.CalculatorTest.testSub"]
	if subCov == nil {
		t.Fatal("no coverage for testSub")
	}
	if equalInts(addCov["src/Calculator.php"], subCov["src/Calculator.php"]) {
		t.Error("testAdd and testSub cover identical lines — per-test isolation broken")
	}

	colFile, err := os.Open(colPath)
	if err != nil {
		t.Fatal(err)
	}
	defer colFile.Close()
	mapping, err := (phpunit.Adapter{}).ParseTestList(colFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(mapping) != 3 {
		t.Errorf("collected %d tests, want 3: %v", len(mapping), mapping)
	}
	if mapping["Demo.Tests.CalculatorTest.testAdd"] != `Demo\Tests\CalculatorTest::testAdd` {
		t.Errorf("mapping = %v", mapping)
	}
}

func testNames(doc phpunit.CoverJSON) []string {
	out := make([]string, 0, len(doc.Tests))
	for k := range doc.Tests {
		out = append(out, k)
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
