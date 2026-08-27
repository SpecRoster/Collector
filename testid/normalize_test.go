package testid

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// coverage.py dynamic contexts (click convention: tests/ not a package)
		{"context plain", "test_basic.test_echo", "test_basic.test_echo"},
		{"context with run suffix", "test_basic.TestC.test_y|run", "test_basic.TestC.test_y"},
		{"context with setup suffix", "test_basic.test_echo|setup", "test_basic.test_echo"},
		// coverage.py contexts (rich convention: tests/ IS a package → dir prefix)
		{"context with package prefix", "tests.test_basic.TestC.test_y", "test_basic.TestC.test_y"},
		{"context deep package prefix", "pkg.sub.tests.test_console.test_print", "test_console.test_print"},
		// pytest nodeids
		{"nodeid simple", "tests/test_basic.py::test_echo", "test_basic.test_echo"},
		{"nodeid with class", "tests/test_basic.py::TestC::test_y", "test_basic.TestC.test_y"},
		{"nodeid with params", "tests/test_basic.py::TestC::test_y[p1]", "test_basic.TestC.test_y"},
		{"nodeid params with brackets content", "tests/test_x.py::test_y[a-b/c.d]", "test_x.test_y"},
		{"nodeid windows path", "tests\\test_basic.py::test_echo", "test_basic.test_echo"},
		{"nodeid no dir", "test_basic.py::test_echo", "test_basic.test_echo"},
		// class-based naming (TestX prefix starts the canonical key)
		{"class first component", "TestSuite.test_a", "TestSuite.test_a"},
		// whitespace robustness
		{"surrounding whitespace", "  tests/test_a.py::test_b  ", "test_a.test_b"},
		// no test_/Test component at all → key passes through intact
		{"no test component", "helpers.fixtures.setup", "helpers.fixtures.setup"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Normalize(c.in); got != c.want {
				t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestNormalizeIsConvergent(t *testing.T) {
	// The whole point: every format of the same test converges on one key.
	forms := []string{
		"tests/test_basic.py::TestC::test_y[p1]",
		"tests.test_basic.TestC.test_y|run",
		"test_basic.TestC.test_y",
	}
	want := "test_basic.TestC.test_y"
	for _, f := range forms {
		if got := Normalize(f); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", f, got, want)
		}
	}
}
