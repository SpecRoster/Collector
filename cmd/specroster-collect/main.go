// Command specroster-collect is the consolidated coverage collector: one
// binary for every supported runner, dispatched by -runner into the
// per-ecosystem handlers under collect/.
//
// It runs inside customer CI and reads customer source, which is why it is
// open. The selection engine — the reverse index, the always-run floor,
// budget-fill, ranking, attribution — is not in this repository at all, and
// so cannot be linked into this binary by accident. That boundary used to
// be a CI check over an import graph; it is now the repository boundary.
//
// The flag surface is the union of every handler's flags; each runner uses
// what it needs and ignores the rest, which is what lets the coverage
// action invoke every runner with one uniform command line.
//
// Usage:
//
//	specroster-collect -runner=<pytest|gotest|dotnet|jest|junit|rspec|cargo|phpunit> \
//	    [-dir .] [-repo-root .] [-project ...] [-o coverage.json] [-collected collected.txt] ...
package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/SpecRoster/Collector/collect/dotnetcover"
	"github.com/SpecRoster/Collector/collect/gocover"
	"github.com/SpecRoster/Collector/collect/jestcover"
	"github.com/SpecRoster/Collector/collect/jvmcover"
	"github.com/SpecRoster/Collector/collect/phpcover"
	"github.com/SpecRoster/Collector/collect/pytestcover"
	"github.com/SpecRoster/Collector/collect/rbcover"
	"github.com/SpecRoster/Collector/collect/rustcover"
)

func main() {
	runner := flag.String("runner", "", "test runner: pytest, gotest, dotnet, jest, junit, rspec, cargo, or phpunit (required)")
	dir := flag.String("dir", ".", "project root to collect from")
	repoRoot := flag.String("repo-root", "", "repository root coverage paths are made relative to (default: -dir; dotnet default: .)")
	out := flag.String("o", "coverage.json", "coverage JSON output path")
	collected := flag.String("collected", "collected.txt", "collected test list output path")
	// dotnet only
	project := flag.String("project", "", "test project directory or .csproj (dotnet; default: discover all under -repo-root)")
	timings := flag.String("timings", "timings.json", "per-test duration output path (dotnet)")
	covMode := flag.String("cov-mode", "msbuild", "coverage mode: msbuild | collector (dotnet)")
	filter := flag.String("filter", "", "VSTest --filter to scope collected tests (dotnet)")
	jobs := flag.Int("jobs", dotnetcover.DefaultJobs(), "tests to cover concurrently (dotnet)")
	// junit only
	sourceRoot := flag.String("source-root", "src/main/java", "source root JaCoCo package paths are joined under (junit)")
	// phpunit only
	phpunitBin := flag.String("phpunit", "vendor/bin/phpunit", "phpunit binary, relative paths resolve against -dir (phpunit)")
	// pytest only
	python := flag.String("python", "python", "python interpreter (pytest)")
	pytestArgs := flag.String("pytest-args", "", "extra arguments passed to pytest, whitespace-split (pytest)")
	flag.Parse()

	if err := dispatch(*runner, *dir, *repoRoot, *out, *collected, *project, *timings, *covMode, *filter, *jobs, *sourceRoot, *phpunitBin, *python, *pytestArgs); err != nil {
		log.Fatal(err)
	}
}

func dispatch(runner, dir, repoRoot, out, collected, project, timings, covMode, filter string, jobs int, sourceRoot, phpunitBin, python, pytestArgs string) error {
	switch runner {
	case "pytest":
		return pytestcover.Run(dir, python, pytestArgs, out, collected)
	case "gotest":
		return gocover.Run(dir, out, collected)
	case "dotnet":
		if repoRoot == "" {
			repoRoot = "." // dotnetcover's historical default
		}
		return dotnetcover.Run(project, repoRoot, out, collected, timings, covMode, filter, jobs)
	case "jest":
		return jestcover.Run(dir, repoRoot, out, collected)
	case "junit":
		return jvmcover.Run(dir, repoRoot, sourceRoot, out, collected)
	case "rspec":
		return rbcover.Run(dir, repoRoot, out, collected)
	case "cargo":
		return rustcover.Run(dir, repoRoot, out, collected)
	case "phpunit":
		return phpcover.Run(dir, repoRoot, phpunitBin, out, collected)
	case "":
		return fmt.Errorf("-runner is required (pytest|gotest|dotnet|jest|junit|rspec|cargo|phpunit)")
	default:
		return fmt.Errorf("unknown -runner %q (want pytest|gotest|dotnet|jest|junit|rspec|cargo|phpunit)", runner)
	}
}
