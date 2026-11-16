package build

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// TestEvent is one event of the `go test -json` / `go tool test2json` stream
// (see `go doc cmd/test2json`). Build events (Action "build-output" and
// "build-fail") carry ImportPath instead of Package.
type TestEvent struct {
	Action      string
	Package     string
	Test        string
	Elapsed     float64
	Output      string
	ImportPath  string
	FailedBuild string
}

// Synthesized test case names for a package that did not get as far as
// reporting its own tests.
const (
	buildFailedCase   = "[build failed]"
	packageFailedCase = "[package failed]"
)

// TestReport collects test results from test2json events, one package per
// test binary. It renders them as a JUnit XML report and as a summary.
type TestReport struct {
	// pkgName maps the package path a `go` command reports to the one shown
	// in the report; nil keeps it unchanged.
	pkgName func(string) string

	packages    map[string]*packageResult
	buildOutput map[string]*strings.Builder // ImportPath -> compiler output
}

type packageResult struct {
	name    string
	action  string // the package's final action: pass, fail or skip
	elapsed float64
	output  strings.Builder // output that belongs to no test
	tests   []*testResult
	byName  map[string]*testResult
	// buildFailure is the compiler output of a package that did not build.
	buildFailure *string
}

type testResult struct {
	name     string
	action   string // pass, fail or skip; "" while still running
	elapsed  float64
	output   strings.Builder
	reported bool // the test printed its own "--- PASS/FAIL/SKIP" line
}

// NewTestReport returns an empty report. pkgName maps reported package paths
// to display names; nil keeps them unchanged.
func NewTestReport(pkgName func(string) string) *TestReport {
	return &TestReport{
		pkgName:     pkgName,
		packages:    map[string]*packageResult{},
		buildOutput: map[string]*strings.Builder{},
	}
}

func (r *TestReport) pkg(path string) *packageResult {
	if p, ok := r.packages[path]; ok {
		return p
	}
	name := path
	if r.pkgName != nil {
		name = r.pkgName(path)
	}
	p := &packageResult{name: name, byName: map[string]*testResult{}}
	r.packages[path] = p
	return p
}

// framingLine matches the lines a test runner prints around a test, which
// are not part of what the test itself logged.
var framingLine = regexp.MustCompile(`^\s*(=== (RUN|PAUSE|CONT|NAME)|--- (PASS|FAIL|SKIP):)`)

// reportLine matches a test's own result line, "--- PASS: TestX (0.00s)".
var reportLine = regexp.MustCompile(`^\s*--- (PASS|FAIL|SKIP): (\S+)`)

// Add records one event.
func (r *TestReport) Add(ev TestEvent) {
	switch ev.Action {
	case "build-output":
		b, ok := r.buildOutput[ev.ImportPath]
		if !ok {
			b = &strings.Builder{}
			r.buildOutput[ev.ImportPath] = b
		}
		b.WriteString(ev.Output)
		return
	case "build-fail":
		return
	}
	if ev.Package == "" {
		return
	}
	p := r.pkg(ev.Package)
	if ev.Test == "" {
		switch ev.Action {
		case "output":
			p.output.WriteString(ev.Output)
		case "pass", "fail", "skip":
			p.action, p.elapsed = ev.Action, ev.Elapsed
			if ev.Action == "fail" {
				r.finishFailedPackage(p, ev)
			}
		}
		return
	}
	t, ok := p.byName[ev.Test]
	if !ok {
		t = &testResult{name: ev.Test}
		p.byName[ev.Test] = t
		p.tests = append(p.tests, t)
	}
	switch ev.Action {
	case "output":
		// test2json attributes everything up to the next test's start to
		// the last test, so the runner's closing lines would land in it.
		// They belong to the package once the test has reported.
		if t.reported {
			p.output.WriteString(ev.Output)
			return
		}
		if m := reportLine.FindStringSubmatch(ev.Output); m != nil && m[2] == ev.Test {
			t.reported = true
		}
		if !framingLine.MatchString(ev.Output) {
			t.output.WriteString(ev.Output)
		}
	case "pass", "fail", "skip":
		t.action, t.elapsed = ev.Action, ev.Elapsed
	}
}

// finishFailedPackage makes sure a failed package reports why: a test still
// running when its binary exited failed with it, and a package with no
// failing test failed to build or failed outside its tests.
func (r *TestReport) finishFailedPackage(p *packageResult, ev TestEvent) {
	for _, t := range p.tests {
		if t.action == "" {
			t.action = "fail"
		}
	}
	buildFailed := ev.FailedBuild != "" || strings.Contains(p.output.String(), "[build failed]")
	if buildFailed {
		out := p.output.String()
		if b, ok := r.buildOutput[ev.FailedBuild]; ok {
			out = b.String()
		}
		p.buildFailure = &out
		return
	}
	for _, t := range p.tests {
		if t.action == "fail" {
			return
		}
	}
	p.tests = append(p.tests, &testResult{name: packageFailedCase, action: "error"})
}

// AddBuildFailure records a package that failed to build before any of its
// tests could run.
func (r *TestReport) AddBuildFailure(pkgPath, output string) {
	p := r.pkg(pkgPath)
	p.action = "fail"
	p.buildFailure = &output
}

// Empty reports whether no package has a result.
func (r *TestReport) Empty() bool {
	return len(r.reportedPackages()) == 0
}

// HasFailures reports whether any package failed.
func (r *TestReport) HasFailures() bool {
	for _, p := range r.reportedPackages() {
		if p.action == "fail" {
			return true
		}
	}
	return false
}

// reportedPackages returns the packages that have something to report,
// sorted by name. A package with no tests that was skipped ("no test files")
// has nothing.
func (r *TestReport) reportedPackages() []*packageResult {
	var pkgs []*packageResult
	for _, p := range r.packages {
		if len(p.tests) == 0 && p.buildFailure == nil && p.action != "fail" {
			continue
		}
		pkgs = append(pkgs, p)
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].name < pkgs[j].name })
	return pkgs
}

// packageCounts are the counts of a package's test cases by outcome. A
// package that did not build counts as one error.
type packageCounts struct {
	tests, passed, failures, errors, skipped int
}

func (p *packageResult) counts() packageCounts {
	if p.buildFailure != nil {
		return packageCounts{tests: 1, errors: 1}
	}
	var c packageCounts
	for _, t := range p.tests {
		c.tests++
		switch t.action {
		case "pass":
			c.passed++
		case "skip":
			c.skipped++
		case "error":
			c.errors++
		default:
			c.failures++
		}
	}
	return c
}

// failureMessage is the one-line reason a test failed: the first error the
// test reported, or a panic.
func failureMessage(t *testResult, p *packageResult) string {
	for _, line := range strings.Split(t.output.String(), "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"ERROR: ", "FATAL: "} {
			if strings.HasPrefix(line, prefix) {
				return strings.TrimPrefix(line, prefix)
			}
		}
		if strings.HasPrefix(line, "panic: ") {
			return line
		}
	}
	if t.name == packageFailedCase {
		return "the test binary failed outside of any test"
	}
	if t.action == "fail" && t.output.Len() == 0 && hasFailedSubtest(t, p) {
		return "a subtest failed"
	}
	return "test failed"
}

func hasFailedSubtest(t *testResult, p *packageResult) bool {
	for _, sub := range p.tests {
		if strings.HasPrefix(sub.name, t.name+"/") && sub.action == "fail" {
			return true
		}
	}
	return false
}

// JUnit XML, in the shape the common JUnit schema (as read by GitHub, GitLab
// and Jenkins) expects: testsuites > testsuite > testcase.

type junitTestSuites struct {
	XMLName  xml.Name         `xml:"testsuites"`
	Name     string           `xml:"name,attr"`
	Tests    int              `xml:"tests,attr"`
	Failures int              `xml:"failures,attr"`
	Errors   int              `xml:"errors,attr"`
	Skipped  int              `xml:"skipped,attr"`
	Time     string           `xml:"time,attr"`
	Suites   []junitTestSuite `xml:"testsuite"`
}

type junitTestSuite struct {
	Name      string          `xml:"name,attr"`
	Tests     int             `xml:"tests,attr"`
	Failures  int             `xml:"failures,attr"`
	Errors    int             `xml:"errors,attr"`
	Skipped   int             `xml:"skipped,attr"`
	Time      string          `xml:"time,attr"`
	TestCases []junitTestCase `xml:"testcase"`
	SystemOut string          `xml:"system-out,omitempty"`
}

type junitTestCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Skipped   *junitMessage `xml:"skipped"`
	Failure   *junitMessage `xml:"failure"`
	Error     *junitMessage `xml:"error"`
	SystemOut string        `xml:"system-out,omitempty"`
}

type junitMessage struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr,omitempty"`
	Body    string `xml:",chardata"`
}

func junitTime(seconds float64) string { return fmt.Sprintf("%.3f", seconds) }

func (r *TestReport) junit() junitTestSuites {
	all := junitTestSuites{Name: "gala test"}
	var total float64
	for _, p := range r.reportedPackages() {
		c := p.counts()
		suite := junitTestSuite{
			Name:      p.name,
			Tests:     c.tests,
			Failures:  c.failures,
			Errors:    c.errors,
			Skipped:   c.skipped,
			Time:      junitTime(p.elapsed),
			SystemOut: p.output.String(),
		}
		if p.buildFailure != nil {
			suite.TestCases = []junitTestCase{{
				Name:      buildFailedCase,
				Classname: p.name,
				Time:      junitTime(0),
				Error:     &junitMessage{Message: "build failed", Type: "BuildError", Body: *p.buildFailure},
			}}
		}
		for _, t := range p.tests {
			if p.buildFailure != nil {
				break
			}
			tc := junitTestCase{Name: t.name, Classname: p.name, Time: junitTime(t.elapsed)}
			out := t.output.String()
			switch t.action {
			case "pass":
				tc.SystemOut = out
			case "skip":
				tc.Skipped = &junitMessage{Message: "skipped", Body: out}
			case "error":
				tc.Error = &junitMessage{Message: failureMessage(t, p), Body: p.output.String()}
			default:
				tc.Failure = &junitMessage{Message: failureMessage(t, p), Body: out}
			}
			suite.TestCases = append(suite.TestCases, tc)
		}
		all.Tests += c.tests
		all.Failures += c.failures
		all.Errors += c.errors
		all.Skipped += c.skipped
		total += p.elapsed
		all.Suites = append(all.Suites, suite)
	}
	all.Time = junitTime(total)
	return all
}

// WriteJUnit writes the report as JUnit XML.
func (r *TestReport) WriteJUnit(w io.Writer) error {
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(r.junit()); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// WriteJUnitFile writes the report as JUnit XML to path, creating its
// directory if needed.
func (r *TestReport) WriteJUnitFile(path string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("writing JUnit report: %w", err)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("writing JUnit report: %w", err)
	}
	if err := r.WriteJUnit(f); err != nil {
		f.Close()
		return fmt.Errorf("writing JUnit report: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("writing JUnit report: %w", err)
	}
	return nil
}

// WriteSummary writes the end-of-run summary: the counts per package, then
// every failure with its reason.
func (r *TestReport) WriteSummary(w io.Writer) {
	pkgs := r.reportedPackages()
	if len(pkgs) == 0 {
		return
	}
	width := 0
	for _, p := range pkgs {
		width = max(width, len(p.name))
	}
	var total packageCounts
	fmt.Fprintln(w)
	fmt.Fprintln(w, "=== SUMMARY ===")
	for _, p := range pkgs {
		status := "ok  "
		if p.action == "fail" {
			status = "FAIL"
		}
		c := p.counts()
		total.passed += c.passed
		total.failures += c.failures
		total.errors += c.errors
		total.skipped += c.skipped
		if p.buildFailure != nil {
			fmt.Fprintf(w, "%s  %-*s  build failed\n", status, width, p.name)
			continue
		}
		fmt.Fprintf(w, "%s  %-*s  %d passed, %d failed, %d skipped\n", status, width, p.name, c.passed, c.failures+c.errors, c.skipped)
	}

	var failures []string
	for _, p := range pkgs {
		if p.buildFailure != nil {
			failures = append(failures, fmt.Sprintf("  %s: build failed\n%s", p.name, indent(*p.buildFailure, "      ")))
			continue
		}
		for _, t := range p.tests {
			if t.action != "fail" && t.action != "error" {
				continue
			}
			// A test that failed only because a subtest did is listed
			// through that subtest.
			if t.output.Len() == 0 && hasFailedSubtest(t, p) {
				continue
			}
			failures = append(failures, fmt.Sprintf("  %s: %s\n      %s\n", p.name, t.name, failureMessage(t, p)))
		}
	}
	if len(failures) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Failures:")
		for _, f := range failures {
			fmt.Fprint(w, f)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%d passed, %d failed, %d skipped in %d package(s)\n",
		total.passed, total.failures+total.errors, total.skipped, len(pkgs))
}

func indent(s, prefix string) string {
	var sb strings.Builder
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		sb.WriteString(prefix)
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	return sb.String()
}

// testEventPrinter reproduces on the terminal what the `go` command would
// have printed without -json, from its events. Build output goes to stderr.
//
// With stream set, every line is printed as it arrives, as `go test -v` (or
// a test binary run directly) does. Otherwise a package's output is held
// until it finishes: a failed package prints all of it, a passed one only
// its closing "ok" line, as plain `go test` does.
type testEventPrinter struct {
	stdout, stderr io.Writer
	stream         bool
	held           map[string][]string
	// failed records that a package failed.
	failed bool
}

func (pr *testEventPrinter) print(ev TestEvent) {
	if ev.Test == "" && ev.Action == "fail" {
		pr.failed = true
	}
	switch {
	case ev.Action == "build-output":
		fmt.Fprint(pr.stderr, ev.Output)
	case ev.Action == "output" && pr.stream:
		fmt.Fprint(pr.stdout, ev.Output)
	case ev.Action == "output":
		if pr.held == nil {
			pr.held = map[string][]string{}
		}
		pr.held[ev.Package] = append(pr.held[ev.Package], ev.Output)
	case ev.Test == "" && !pr.stream && (ev.Action == "pass" || ev.Action == "fail" || ev.Action == "skip"):
		held := pr.held[ev.Package]
		delete(pr.held, ev.Package)
		if ev.Action != "fail" && len(held) > 0 {
			held = held[len(held)-1:]
		}
		for _, line := range held {
			fmt.Fprint(pr.stdout, line)
		}
	}
}

// consumeTestEvents reads a test2json stream from r, adding each event to
// report and printing it with pr. A line that is not an event is printed
// as is.
func consumeTestEvents(r io.Reader, report *TestReport, pr *testEventPrinter) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		var ev TestEvent
		if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &ev) != nil {
			fmt.Fprintln(pr.stdout, string(line))
			continue
		}
		report.Add(ev)
		pr.print(ev)
	}
	return sc.Err()
}
