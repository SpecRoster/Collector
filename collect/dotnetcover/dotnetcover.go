// Package dotnetcover collects PER-TEST coverage for a .NET
// test project — the collection half of the dotnet RunnerAdapter. Like Go,
// .NET has no per-test coverage contexts, so the collector runs each test
// in its own `dotnet test --filter` invocation under coverlet.
//
// Two coverage modes (-cov-mode), because real .NET repos split between the
// two coverlet integrations:
//
//	msbuild   (default) coverlet.msbuild: /p:CollectCoverage=true, native
//	          JSON. The test project must reference coverlet.msbuild.
//	collector coverlet.collector: --collect "XPlat Code Coverage", Cobertura
//	          XML. The test project must reference coverlet.collector. This
//	          is the more common modern setup.
//
// Output: the dotnet adapter's coverage JSON (specroster/dotnet-cover/v1)
// and a collected list of "Namespace.Class::Method" native IDs.
//
// Usage:
//
//	specroster-dotnetcover [-project path/to/Tests.csprojdir] \
//	    [-repo-root .] [-cov-mode msbuild|collector] \
//	    [-o coverage.json] [-collected collected.txt]
//
// With no -project, every test project under -repo-root (any .csproj
// referencing Microsoft.NET.Test.Sdk) is discovered and collected; results
// merge into one snapshot. Display-name parsing assumes the default xUnit
// naming.
package dotnetcover

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/SpecRoster/Collector/runner/dotnet"
)

// Run collects per-test coverage for the .NET test project(s), writing the
// coverage JSON, collected test list, and per-test timings.
func Run(project, repoRoot, out, collected, timings, covMode, filter, only, framework string, listOnly bool, jobs int) error {
	return run(project, repoRoot, out, collected, timings, covMode, filter, only, framework, listOnly, jobs)
}

// DefaultJobs is the default -jobs concurrency (exported for the cmd wrappers).
func DefaultJobs() int {
	return defaultJobs()
}

// defaultJobs leaves headroom: dotnet test + coverage is memory- and
// CPU-hungry, so we don't saturate every core.
func defaultJobs() int {
	n := runtime.NumCPU() - 2
	if n < 1 {
		n = 1
	}
	if n > 8 {
		n = 8
	}
	return n
}

func run(project, repoRoot, outPath, collectedPath, timingsPath, covMode, filter, onlyPath, framework string, listOnly bool, jobs int) error {
	if jobs < 1 {
		jobs = 1
	}
	switch covMode {
	case "msbuild", "collector":
	default:
		return fmt.Errorf("invalid -cov-mode %q (want msbuild or collector)", covMode)
	}
	// coverlet.msbuild instruments the assemblies in place (shared bin/), so
	// concurrent per-test runs corrupt each other's coverage. Only the
	// collector (runtime data collector) is parallel-safe.
	if covMode == "msbuild" && jobs > 1 {
		fmt.Fprintln(os.Stderr, "note: -cov-mode msbuild instruments shared assemblies in place; forcing -jobs=1 (use -cov-mode collector for parallel collection)")
		jobs = 1
	}
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return err
	}

	var projects []string
	if project != "" {
		projects = []string{project}
	} else {
		if projects, err = discoverTestProjects(repoRoot); err != nil {
			return err
		}
		if len(projects) == 0 {
			return fmt.Errorf("no test projects (csproj referencing Microsoft.NET.Test.Sdk) found under %s", repoRoot)
		}
		fmt.Fprintf(os.Stderr, "discovered %d test project(s): %s\n", len(projects), strings.Join(projects, ", "))
	}

	// An incremental run collects coverage for only the tests SpecRoster asked
	// for, but still reports the COMPLETE inventory below — listing is cheap
	// where collection is not, and the server needs the whole suite to tell a
	// test it did not re-collect from one that no longer exists.
	only, err := readOnly(onlyPath)
	if err != nil {
		return err
	}

	doc := dotnet.CoverJSON{Format: dotnet.CoverageFormat, Tests: map[string]map[string][]int{}}
	timings := timingDoc{Format: timingFormat, DurationsMs: map[string]int64{}}
	var natives []string
	total, failures := 0, 0
	for _, proj := range projects {
		tfm, err := resolveFramework(proj, framework)
		if err != nil {
			return err
		}

		// One build per project up front; per-test runs reuse it (--no-build).
		buildArgs := append([]string{"build", proj, "--nologo", "-v", "q"}, frameworkArgs(tfm)...)
		if out, err := exec.Command("dotnet", buildArgs...).CombinedOutput(); err != nil {
			return fmt.Errorf("dotnet build %s: %w\n%s", proj, err, out)
		}
		fqns, err := listTests(proj, filter, tfm)
		if err != nil {
			return err
		}
		total += len(fqns)

		// The inventory is every test that EXISTS, recorded before any
		// collection is attempted. Deriving it from collection results
		// instead would drop a test that failed to produce coverage, and the
		// server would read that absence as "this test was deleted".
		for _, fqn := range fqns {
			if class, method, ok := splitFQN(fqn); ok {
				natives = append(natives, class+"::"+method)
			}
		}

		toCover := fqns
		if listOnly {
			// Inventory without collection. This is what makes incremental
			// planning possible: the server needs to know the whole suite
			// before it can say which slice of it went stale, and listing is
			// orders of magnitude cheaper than collecting.
			toCover = nil
		} else if only != nil {
			toCover = toCover[:0:0]
			for _, fqn := range fqns {
				if _, want := only[fqn]; want {
					toCover = append(toCover, fqn)
				}
			}
		}
		covered, failed := coverProject(proj, toCover, absRoot, covMode, tfm, jobs, &doc, timings.DurationsMs)
		failures += failed
		fmt.Fprintf(os.Stderr, "project %s: covered %d/%d discovered (%d failed)\n", proj, covered, len(fqns), failed)
	}
	if total == 0 {
		return fmt.Errorf("no tests discovered in %s", strings.Join(projects, ", "))
	}
	if failures > 0 {
		fmt.Fprintf(os.Stderr, "WARNING: %d test(s) failed to cover and were skipped (e.g. need infrastructure); coverage is partial\n", failures)
	}
	if len(doc.Tests) == 0 && !listOnly && (only == nil || len(only) > 0) {
		// With -only naming zero tests there is genuinely nothing to collect:
		// the suite is unchanged and fully profiled. That is the success case
		// for an incremental run, not a misconfiguration.
		return fmt.Errorf("no tests produced coverage (%d discovered, all failed) — check -cov-mode and that the project references the matching coverlet package", total)
	}

	if listOnly {
		sort.Strings(natives)
		return os.WriteFile(collectedPath, []byte(strings.Join(natives, "\n")+"\n"), 0o644)
	}

	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(doc); err != nil {
		return err
	}
	if timingsPath != "" {
		tf, err := os.Create(timingsPath)
		if err != nil {
			return err
		}
		defer tf.Close()
		if err := json.NewEncoder(tf).Encode(timings); err != nil {
			return err
		}
	}
	sort.Strings(natives)
	return os.WriteFile(collectedPath, []byte(strings.Join(natives, "\n")+"\n"), 0o644)
}

// timingFormat tags the per-test duration sidecar.
const timingFormat = "specroster/timing/v1"

// timingDoc is the duration sidecar: canonical test ID → wall-clock ms the
// test itself took (parsed from the .trx, so it excludes dotnet host startup).
type timingDoc struct {
	Format      string           `json:"format"`
	DurationsMs map[string]int64 `json:"durations_ms"`
}

// coverResult carries one test's outcome back from a worker.
type coverResult struct {
	fqn   string
	files map[string][]int
	durMs int64
	err   error
}

// coverProject covers every fqn in the project with a pool of `jobs` workers
// (per-test coverage is N independent `dotnet test` runs — the bottleneck).
// Results are merged on the calling goroutine, so doc/natives/timings need no
// locking. A test that fails to cover is logged and skipped, not fatal.
func coverProject(project string, fqns []string, absRoot, covMode, tfm string, jobs int,
	doc *dotnet.CoverJSON, timings map[string]int64) (covered, failed int) {

	work := make(chan string)
	results := make(chan coverResult)
	var wg sync.WaitGroup
	for i := 0; i < jobs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for fqn := range work {
				files, dur, err := coverOneTest(project, fqn, absRoot, covMode, tfm)
				results <- coverResult{fqn, files, dur, err}
			}
		}()
	}
	go func() {
		for _, fqn := range fqns {
			if _, _, ok := splitFQN(fqn); ok {
				work <- fqn
			}
		}
		close(work)
	}()
	go func() { wg.Wait(); close(results) }()

	var done int64
	for r := range results {
		n := atomic.AddInt64(&done, 1)
		if r.err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "[%d/%d] FAILED %s: %s\n", n, len(fqns), r.fqn, firstLine(r.err.Error()))
			continue
		}
		covered++
		if len(r.files) > 0 {
			doc.Tests[r.fqn] = r.files
		}
		if r.durMs > 0 {
			timings[r.fqn] = r.durMs
		}
		fmt.Fprintf(os.Stderr, "[%d/%d] %s (%d files, %dms)\n", n, len(fqns), r.fqn, len(r.files), r.durMs)
	}
	return covered, failed
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// discoverTestProjects finds csproj files referencing the VSTest platform.
func discoverTestProjects(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "bin", "obj", ".git", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".csproj") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "Microsoft.NET.Test.Sdk") {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// listTests parses `dotnet test --list-tests` output: indented display
// names after the "The following Tests are available:" banner. Default
// xUnit display names ARE the FQN (theories carry "(args)", stripped and
// deduped here).
func listTests(project, filter, tfm string) ([]string, error) {
	// NOTE: `dotnet test --list-tests` does NOT honor --filter (it returns an
	// empty list when a filter is present, depending on the test host). So we
	// always list everything and apply the filter in Go (matchFilter). The
	// per-test RUN still uses --filter, which works for running.
	args := append([]string{"test", project, "--list-tests", "--no-build", "--nologo", "-v", "q"}, frameworkArgs(tfm)...)
	out, err := exec.Command("dotnet", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("dotnet test --list-tests: %w\n%s", err, out)
	}
	seen := map[string]bool{}
	var fqns []string
	banner := false
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "Tests are available") {
			banner = true
			continue
		}
		if !banner {
			continue
		}
		name := strings.TrimSpace(line)
		if name == "" || !strings.Contains(name, ".") {
			continue
		}
		if i := strings.Index(name, "("); i >= 0 {
			name = name[:i]
		}
		if !seen[name] && matchFilter(name, filter) {
			seen[name] = true
			fqns = append(fqns, name)
		}
	}
	sort.Strings(fqns)
	return fqns, nil
}

// matchFilter applies a (subset of) VSTest --filter syntax to a canonical FQN
// in Go, since --list-tests can't filter for us. Supports OR'd terms split on
// '|': "FullyQualifiedName~X" (contains), "FullyQualifiedName=X" (exact), or a
// bare substring. Empty filter matches everything.
func matchFilter(fqn, filter string) bool {
	if strings.TrimSpace(filter) == "" {
		return true
	}
	for _, term := range strings.Split(filter, "|") {
		term = strings.TrimSpace(term)
		switch {
		case strings.HasPrefix(term, "FullyQualifiedName~"):
			if strings.Contains(fqn, strings.TrimPrefix(term, "FullyQualifiedName~")) {
				return true
			}
		case strings.HasPrefix(term, "FullyQualifiedName="):
			if fqn == strings.TrimPrefix(term, "FullyQualifiedName=") {
				return true
			}
		default:
			if strings.Contains(fqn, term) {
				return true
			}
		}
	}
	return false
}

func splitFQN(fqn string) (class, method string, ok bool) {
	i := strings.LastIndex(fqn, ".")
	if i <= 0 || i == len(fqn)-1 {
		return "", "", false
	}
	return fqn[:i], fqn[i+1:], true
}

// coverOneTest runs a single test with coverlet collection and returns
// repo-relative file → covered lines, plus the test's own duration in ms
// (parsed from the .trx, so it excludes dotnet host startup — the number that
// matters for "what fits in a time budget").
func coverOneTest(project, fqn, absRoot, covMode, tfm string) (map[string][]int, int64, error) {
	tmp, err := os.MkdirTemp("", "trdotnet-*")
	if err != nil {
		return nil, 0, err
	}
	defer os.RemoveAll(tmp)

	// A .trx logger in both modes gives us the per-test duration for free.
	args := []string{"test", project, "--no-build", "--nologo", "-v", "q",
		"--filter", "FullyQualifiedName~" + fqn,
		"--results-directory", tmp,
		"--logger", "trx;LogFileName=results.trx"}
	args = append(args, frameworkArgs(tfm)...)

	var files map[string][]int
	if covMode == "collector" {
		args = append(args, "--collect", "XPlat Code Coverage")
		if out, err := exec.Command("dotnet", args...).CombinedOutput(); err != nil {
			return nil, 0, fmt.Errorf("dotnet test (collector): %w\n%s", err, out)
		}
		xmlPath, err := findCoberturaFile(tmp)
		if err != nil {
			return nil, 0, err
		}
		if files, err = parseCobertura(xmlPath, absRoot); err != nil {
			return nil, 0, err
		}
	} else {
		covPath := filepath.Join(tmp, "cov.json")
		args = append(args, "/p:CollectCoverage=true", "/p:CoverletOutputFormat=json", "/p:CoverletOutput="+covPath)
		if out, err := exec.Command("dotnet", args...).CombinedOutput(); err != nil {
			return nil, 0, fmt.Errorf("dotnet test: %w\n%s", err, out)
		}
		if files, err = parseCoverlet(covPath, absRoot); err != nil {
			return nil, 0, err
		}
	}
	return files, trxDurationMs(tmp), nil
}

// trxDurationMs sums the durations of all UnitTestResults in the .trx under
// dir (a per-test invocation may run several theory rows). Best-effort: 0 if
// no .trx or unparsable. The trx default namespace is ignored by Go's xml
// (it matches on local element names).
func trxDurationMs(dir string) int64 {
	var trx string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".trx") {
			trx = p
			return filepath.SkipAll
		}
		return nil
	})
	if trx == "" {
		return 0
	}
	data, err := os.ReadFile(trx)
	if err != nil {
		return 0
	}
	var run struct {
		Results struct {
			UnitTestResults []struct {
				Duration string `xml:"duration,attr"`
			} `xml:"UnitTestResult"`
		} `xml:"Results"`
	}
	if xml.Unmarshal(data, &run) != nil {
		return 0
	}
	var total int64
	for _, r := range run.Results.UnitTestResults {
		total += parseTrxDuration(r.Duration)
	}
	return total
}

// parseTrxDuration parses a trx "HH:MM:SS.fffffff" duration into milliseconds.
func parseTrxDuration(s string) int64 {
	if s == "" {
		return 0
	}
	var h, m int
	var sec float64
	if _, err := fmt.Sscanf(s, "%d:%d:%f", &h, &m, &sec); err != nil {
		return 0
	}
	return int64((float64(h)*3600+float64(m)*60+sec)*1000 + 0.5)
}

// findCoberturaFile locates the `*.cobertura.xml` that `--collect "XPlat Code
// Coverage"` writes under a per-run GUID subdirectory of the results dir.
func findCoberturaFile(resultsDir string) (string, error) {
	var found string
	err := filepath.WalkDir(resultsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".cobertura.xml") {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("no .cobertura.xml under %s (is coverlet.collector referenced by the test project?)", resultsDir)
	}
	return found, nil
}

// coberturaXML is the subset of the Cobertura schema coverlet.collector emits.
type coberturaXML struct {
	Sources  []string `xml:"sources>source"`
	Packages []struct {
		Classes []struct {
			Filename string `xml:"filename,attr"`
			Lines    []struct {
				Number int `xml:"number,attr"`
				Hits   int `xml:"hits,attr"`
			} `xml:"lines>line"`
		} `xml:"classes>class"`
	} `xml:"packages>package"`
}

// parseCobertura folds a Cobertura report into repo-relative file → sorted
// covered lines. Coverlet writes class filenames relative to a <source> root
// (occasionally absolute); resolve each against the sources, keep only paths
// inside the repo, and record lines with hits > 0.
func parseCobertura(path, absRoot string) (map[string][]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read cobertura: %w", err)
	}
	var doc coberturaXML
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse cobertura: %w", err)
	}
	lineSets := map[string]map[int]bool{}
	for _, pkg := range doc.Packages {
		for _, cls := range pkg.Classes {
			rel, ok := repoRelPath(cls.Filename, doc.Sources, absRoot)
			if !ok {
				continue // outside the repo (SDK/generated files)
			}
			for _, ln := range cls.Lines {
				if ln.Hits == 0 {
					continue
				}
				if lineSets[rel] == nil {
					lineSets[rel] = map[int]bool{}
				}
				lineSets[rel][ln.Number] = true
			}
		}
	}
	out := map[string][]int{}
	for file, set := range lineSets {
		lines := make([]int, 0, len(set))
		for l := range set {
			lines = append(lines, l)
		}
		sort.Ints(lines)
		out[file] = lines
	}
	return out, nil
}

// repoRelPath resolves a Cobertura class filename (relative to a <source>, or
// absolute) to a repo-relative slash path, reporting false when it falls
// outside absRoot.
func repoRelPath(filename string, sources []string, absRoot string) (string, bool) {
	filename = filepath.FromSlash(filename)
	var candidates []string
	if filepath.IsAbs(filename) {
		candidates = append(candidates, filename)
	} else {
		for _, src := range sources {
			candidates = append(candidates, filepath.Join(src, filename))
		}
		// Fall back to treating it as already relative to the repo root.
		candidates = append(candidates, filepath.Join(absRoot, filename))
	}
	for _, abs := range candidates {
		rel, err := filepath.Rel(absRoot, abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		return filepath.ToSlash(rel), true
	}
	return "", false
}

// coverlet JSON: module → document(abs path) → class → method → {Lines: {line: hits}}.
type coverletDoc map[string]map[string]map[string]map[string]struct {
	Lines map[string]int `json:"Lines"`
}

func parseCoverlet(path, absRoot string) (map[string][]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read coverlet output: %w", err)
	}
	var doc coverletDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse coverlet output: %w", err)
	}
	lineSets := map[string]map[int]bool{}
	for _, docs := range doc {
		for docPath, classes := range docs {
			rel, err := filepath.Rel(absRoot, docPath)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue // outside the repo (SDK/generated files)
			}
			rel = filepath.ToSlash(rel)
			for _, methods := range classes {
				for _, m := range methods {
					for lineStr, hits := range m.Lines {
						if hits == 0 {
							continue
						}
						line, err := strconv.Atoi(lineStr)
						if err != nil {
							continue
						}
						if lineSets[rel] == nil {
							lineSets[rel] = map[int]bool{}
						}
						lineSets[rel][line] = true
					}
				}
			}
		}
	}
	out := map[string][]int{}
	for file, set := range lineSets {
		lines := make([]int, 0, len(set))
		for l := range set {
			lines = append(lines, l)
		}
		sort.Ints(lines)
		out[file] = lines
	}
	return out, nil
}

// readOnly loads the set of canonical test IDs an incremental run was asked
// to re-collect, one per line. An empty path means "collect everything",
// which is a FULL run; a present-but-empty file means "collect nothing",
// which is a legitimate incremental no-op on an unchanged repository. Those
// two must not collapse into one another.
func readOnly(path string) (map[string]struct{}, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read -only list: %w", err)
	}
	only := map[string]struct{}{}
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			only[line] = struct{}{}
		}
	}
	return only, nil
}

// frameworkArgs renders the target-framework selector, or nothing when the
// project targets exactly one.
func frameworkArgs(tfm string) []string {
	if tfm == "" {
		return nil
	}
	return []string{"-f", tfm}
}

// resolveFramework decides which target framework to drive a project with.
//
// A multi-targeting project — most serious .NET libraries — builds every
// framework in its list unless told otherwise. That is slow at best, and on
// a machine without every SDK installed it fails outright or lists no tests,
// which surfaced as the useless "no tests discovered". Worse, discovering
// nothing looks identical to a project with no tests.
//
// So: honour an explicit choice, accept a single-target project as-is, and
// REFUSE a multi-target project with no choice made — naming the frameworks
// found, because the fix is for the caller to pick one and there is no safe
// default. Picking silently would collect coverage against a framework the
// customer does not ship.
func resolveFramework(project, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	out, err := exec.Command("dotnet", "msbuild", project, "-getProperty:TargetFrameworks", "-v:q", "--nologo").Output()
	if err != nil {
		// Older SDKs lack -getProperty. Carry on as a single-target project:
		// the build will say so more clearly than we can here.
		return "", nil
	}
	list := strings.TrimSpace(string(out))
	if list == "" {
		return "", nil // single-target
	}
	var tfms []string
	for _, f := range strings.Split(list, ";") {
		if f = strings.TrimSpace(f); f != "" {
			tfms = append(tfms, f)
		}
	}
	if len(tfms) <= 1 {
		return strings.Join(tfms, ""), nil
	}
	return "", fmt.Errorf("%s targets %d frameworks (%s); pass -framework to choose one — collecting against the wrong framework would profile code you do not ship",
		project, len(tfms), strings.Join(tfms, ", "))
}
