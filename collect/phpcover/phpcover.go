// Package phpcover collects PER-TEST coverage for a PHPUnit
// project — the collection half of the phpunit RunnerAdapter. PHPUnit has
// no per-test coverage contexts, so the collector runs each test in its
// own `phpunit --filter` invocation with Clover XML collection. Every
// PHPUnit invocation runs with XDEBUG_MODE=coverage (a coverage driver —
// Xdebug or PCOV — must be installed).
//
// Output: the phpunit adapter's coverage JSON (specroster/php-cover/v1)
// and a collected list of "Ns\Class::method" native IDs.
//
// Usage:
//
//	specroster-phpcover [-dir path/to/project] [-repo-root .] \
//	    [-phpunit vendor/bin/phpunit] [-o coverage.json] [-collected collected.txt]
//
// -dir is the PHP project root (where phpunit.xml lives); -repo-root
// defaults to -dir and is what coverage paths are made relative to.
package phpcover

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SpecRoster/Collector/covtypes"
	"github.com/SpecRoster/Collector/runner/phpunit"
)

// Run collects per-test coverage for the PHPUnit project at dir. An empty
// repoRoot defaults to dir.
func Run(dir, repoRoot, phpunitBin, out, collected string) error {
	if repoRoot == "" {
		repoRoot = dir
	}
	return run(dir, repoRoot, phpunitBin, out, collected)
}

func run(dir, repoRoot, phpunitBin, outPath, collectedPath string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return err
	}
	// A relative binary path ("vendor/bin/phpunit") is a project-local
	// install: resolve it against the project dir, not the process cwd.
	bin := phpunitBin
	if !filepath.IsAbs(bin) && strings.ContainsAny(bin, `/\`) {
		bin = filepath.Join(absDir, bin)
	}

	natives, err := listTests(bin, absDir)
	if err != nil {
		return err
	}
	if len(natives) == 0 {
		return fmt.Errorf("no tests discovered in %s", dir)
	}

	layout := (phpunit.Adapter{}).Layout("", "")
	doc := phpunit.CoverJSON{Format: phpunit.CoverageFormat, Tests: map[string]map[string][]int{}}
	for _, native := range natives {
		canonical := (phpunit.Adapter{}).NormalizeNative(native)
		if canonical == "" {
			continue
		}
		files, err := coverOneTest(bin, absDir, absRoot, native, layout)
		if err != nil {
			return fmt.Errorf("cover %s: %w", native, err)
		}
		if len(files) > 0 {
			doc.Tests[canonical] = files
		}
		fmt.Fprintf(os.Stderr, "covered %s (%d files)\n", canonical, len(files))
	}

	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(doc); err != nil {
		return err
	}
	sort.Strings(natives)
	return os.WriteFile(collectedPath, []byte(strings.Join(natives, "\n")+"\n"), 0o644)
}

// listTests parses `phpunit --list-tests` output: " - Ns\Class::method"
// lines (data-provider cases carry a " with data set" or "#N" suffix —
// stripped and deduped here, one entry per method).
func listTests(bin, dir string) ([]string, error) {
	cmd := exec.Command(bin, "--list-tests")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "XDEBUG_MODE=coverage")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("phpunit --list-tests: %w\n%s", err, out)
	}
	seen := map[string]bool{}
	var natives []string
	for _, line := range strings.Split(string(out), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "- ")
		if !ok {
			continue
		}
		class, method, ok := strings.Cut(strings.TrimSpace(rest), "::")
		if !ok || class == "" {
			continue
		}
		method = stripDataSet(method)
		if method == "" {
			continue
		}
		native := class + "::" + method
		if !seen[native] {
			seen[native] = true
			natives = append(natives, native)
		}
	}
	sort.Strings(natives)
	return natives, nil
}

// stripDataSet drops a PHPUnit data-provider suffix from a method name
// (`m with data set #0`, `m with data set "two"`, or the compact "m#0").
func stripDataSet(s string) string {
	if i := strings.Index(s, " with data set"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "#"); i >= 0 && isDigits(s[i+1:]) {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// oneTestFilter builds the --filter regex selecting exactly one method
// (including all its data-provider cases — PHPUnit matches the pattern
// against `Ns\Class::method[ with data set ...]`). Namespace backslashes
// are regex-escaped.
func oneTestFilter(native string) string {
	class, method, _ := strings.Cut(native, "::")
	return "^" + strings.ReplaceAll(class, `\`, `\\`) + "::" + method + `(?: with data set .*)?$`
}

// coverOneTest runs a single test with Clover collection and returns
// repo-relative file → covered lines.
func coverOneTest(bin, dir, absRoot, native string, layout covtypes.Layout) (map[string][]int, error) {
	tmp, err := os.MkdirTemp("", "trphp-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	cloverPath := filepath.Join(tmp, "clover.xml")

	cmd := exec.Command(bin, "--filter", oneTestFilter(native), "--coverage-clover", cloverPath)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "XDEBUG_MODE=coverage")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("phpunit --filter: %w\n%s", err, out)
	}
	return parseClover(cloverPath, absRoot, layout)
}

// Clover XML: <project> holds <file name="/abs/path.php"> elements (some
// nested under <package>), each with <line num="7" type="stmt" count="1"/>
// children. Covered = type "stmt" with count > 0.
type cloverXML struct {
	Project struct {
		Files    []cloverFile `xml:"file"`
		Packages []struct {
			Files []cloverFile `xml:"file"`
		} `xml:"package"`
	} `xml:"project"`
}

type cloverFile struct {
	Name  string `xml:"name,attr"`
	Lines []struct {
		Num   int    `xml:"num,attr"`
		Type  string `xml:"type,attr"`
		Count int    `xml:"count,attr"`
	} `xml:"line"`
}

func parseClover(path, absRoot string, layout covtypes.Layout) (map[string][]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read clover output: %w", err)
	}
	var doc cloverXML
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse clover output: %w", err)
	}
	files := doc.Project.Files
	for _, pkg := range doc.Project.Packages {
		files = append(files, pkg.Files...)
	}
	lineSets := map[string]map[int]bool{}
	for _, file := range files {
		rel, err := filepath.Rel(absRoot, file.Name)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue // outside the repo (vendor symlinks, generated files)
		}
		rel = filepath.ToSlash(rel)
		if layout.ClassifyFile(rel) != covtypes.KindSource {
			continue // test files and non-PHP artifacts
		}
		for _, ln := range file.Lines {
			if ln.Type != "stmt" || ln.Count <= 0 {
				continue
			}
			if lineSets[rel] == nil {
				lineSets[rel] = map[int]bool{}
			}
			lineSets[rel][ln.Num] = true
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
