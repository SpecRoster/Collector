package dotnetcover

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestFrameworkArgs(t *testing.T) {
	if got := frameworkArgs(""); got != nil {
		t.Errorf("frameworkArgs(\"\") = %v, want nil — a single-target project must not be constrained", got)
	}
	if got, want := frameworkArgs("net8.0"), []string{"-f", "net8.0"}; !reflect.DeepEqual(got, want) {
		t.Errorf("frameworkArgs = %v, want %v", got, want)
	}
}

func TestResolveFrameworkHonoursAnExplicitChoice(t *testing.T) {
	// No project is consulted when the caller has already chosen, so this
	// holds without an SDK present.
	got, err := resolveFramework("does/not/exist.csproj", "net8.0")
	if err != nil || got != "net8.0" {
		t.Errorf("resolveFramework = (%q, %v), want (net8.0, nil)", got, err)
	}
}

// A multi-targeting project with no choice made must fail loudly. Silently
// building every framework is slow at best, and on a machine without every
// SDK it lists no tests — which is indistinguishable from a project that has
// none. That ambiguity is what made this a hard failure on real .NET repos.
func TestResolveFrameworkRefusesAmbiguity(t *testing.T) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet SDK not on PATH")
	}
	_, err := resolveFramework("testdata/multitarget/Multi.csproj", "")
	if err == nil {
		t.Fatal("resolveFramework accepted a multi-target project with no -framework")
	}
	for _, want := range []string{"net8.0", "net9.0", "-framework"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q — the message has to name the options and the fix", err, want)
		}
	}
}

func TestResolveFrameworkAcceptsSingleTarget(t *testing.T) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet SDK not on PATH")
	}
	got, err := resolveFramework("testdata/sample/Demo.Tests/Demo.Tests.csproj", "")
	if err != nil {
		t.Fatalf("resolveFramework on a single-target project: %v", err)
	}
	if got != "" {
		t.Errorf("resolveFramework = %q, want empty — a single-target project needs no selector", got)
	}
}
