package build

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	byName  map[string]*testResult // by test2json name, the latest run
	reruns  map[string]int         // runs after the first, by name
	// buildFailure is the compiler output of a package that did not build.
	buildFailure *string
	// failedParents caches hasFailedSubtest.
	failedParents map[string]bool
}

type testResult struct {
	key      string // the name test2json reports it under
	name     string // the name in the report: key, numbered for a rerun
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
	p := &packageResult{name: name, byName: map[string]*testResult{}, reruns: map[string]int{}}
	r.packages[path] = p
	return p
}

// framing reports whether line is a line the test runner printed around the
// test named name, not one the test logged, and whether it is the test's
// own result line ("--- PASS: name (0.00s)"). Framing is unindented, as
// GALA's test framework prints it, and names the whole test: a name may
// hold spaces.
func framing(line, name string) (isFraming, isResult bool) {
	line = strings.TrimRight(line, "\r\n")
	if len(line) < 10 {
		return false, false
	}
	// Every marker is 10 bytes: "=== RUN   ", "--- PASS: " and the rest.
	marker, rest := line[:10], line[10:]
	switch marker {
	case "=== RUN   ", "=== PAUSE ", "=== CONT  ", "=== NAME  ":
		return rest == name, false
	case "--- PASS: ", "--- FAIL: ", "--- SKIP: ":
		after, ok := strings.CutPrefix(rest, name)
		ok = ok && (after == "" || strings.HasPrefix(after, " ("))
		return ok, ok
	}
	return false, false
}

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
	switch ev.Action {
	case "run", "pass", "fail", "skip":
		p.failedParents = nil // results change: rebuild when next asked
	}
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
	// A second run under a name already used (two t.Run calls with the
	// same name) is a test case of its own, numbered as `go test` numbers
	// them: "case#01".
	if !ok || (ev.Action == "run" && (t.action != "" || t.reported)) {
		name := ev.Test
		if ok {
			p.reruns[ev.Test]++
			name = fmt.Sprintf("%s#%02d", ev.Test, p.reruns[ev.Test])
		}
		t = &testResult{key: ev.Test, name: name}
		p.byName[ev.Test] = t
		p.tests = append(p.tests, t)
	}
	switch ev.Action {
	case "output":
		// test2json attributes everything up to the next test's start to
		// the last test, even once it has reported: what a parent logs after
		// a subtest finished, and the runner's closing lines. They belong to
		// the closest test still running, or to the package.
		owner := p.runningOwner(t)
		if owner == nil {
			p.output.WriteString(ev.Output)
			return
		}
		isFraming, isResult := framing(ev.Output, ev.Test)
		if isResult && owner == t {
			t.reported = true
		}
		if !isFraming {
			owner.output.WriteString(ev.Output)
		}
	case "pass", "fail", "skip":
		t.action, t.elapsed = ev.Action, ev.Elapsed
	}
}

// runningOwner returns t, or when t has already reported, its closest
// ancestor that has not; nil when there is none.
func (p *packageResult) runningOwner(t *testResult) *testResult {
	for name := t.key; t.reported; {
		i := strings.LastIndex(name, "/")
		if i < 0 {
			return nil
		}
		name = name[:i]
		if parent, ok := p.byName[name]; ok {
			t = parent
		}
	}
	return t
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
	if ev.FailedBuild != "" {
		out := p.output.String()
		if b, ok := r.buildOutput[ev.FailedBuild]; ok {
			out = b.String()
		}
		p.addBuildFailure(out)
		return
	}
	for _, t := range p.tests {
		if t.action == "fail" {
			return
		}
	}
	p.tests = append(p.tests, &testResult{key: packageFailedCase, name: packageFailedCase, action: "error"})
}

// AddBuildFailure records a package that failed to build before any of its
// tests could run.
func (r *TestReport) AddBuildFailure(pkgPath, output string) {
	p := r.pkg(pkgPath)
	p.action = "fail"
	p.addBuildFailure(output)
}

// addBuildFailure records output as why the package did not build; a
// second failure keeps the first.
func (p *packageResult) addBuildFailure(output string) {
	if p.buildFailure != nil {
		output = strings.TrimRight(*p.buildFailure, "\n") + "\n" + output
	}
	p.buildFailure = &output
}

// Empty reports whether no package has a result.
func (r *TestReport) Empty() bool {
	for _, p := range r.packages {
		if p.hasResult() {
			return false
		}
	}
	return true
}

// WriteBuildFailureReport writes a JUnit report holding only a failed
// build of the named project: for a run that could not get as far as
// building it, so CI never reads a stale report from an earlier run.
func WriteBuildFailureReport(path, name string, err error) error {
	report := NewTestReport(nil)
	report.AddBuildFailure(name, err.Error())
	return report.WriteJUnitFile(path)
}

// hasResult reports whether the package has something to report. A package
// with no tests that was skipped ("no test files") has nothing.
func (p *packageResult) hasResult() bool {
	return len(p.tests) > 0 || p.buildFailure != nil || p.action == "fail"
}

// reportedPackages returns the packages that have something to report,
// sorted by name.
func (r *TestReport) reportedPackages() []*packageResult {
	var pkgs []*packageResult
	for _, p := range r.packages {
		if p.hasResult() {
			pkgs = append(pkgs, p)
		}
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

func (c *packageCounts) add(o packageCounts) {
	c.tests += o.tests
	c.passed += o.passed
	c.failures += o.failures
	c.errors += o.errors
	c.skipped += o.skipped
}

// packageFailedMessage is the reason given for a package that failed outside
// its tests with no error or panic line to quote.
const packageFailedMessage = "the test binary failed outside of any test"

// failureMessage is the one-line reason a test failed: the first error the
// test reported, or a panic.
func failureMessage(t *testResult, p *packageResult) string {
	out := t.output.String()
	if t.name == packageFailedCase {
		// Its reason is in what the package printed.
		out = p.output.String()
	}
	for line := range strings.Lines(out) {
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
		return packageFailedMessage
	}
	if t.action == "" {
		return "the test did not finish"
	}
	if failedOnlyThroughSubtest(t, p) {
		return "a subtest failed"
	}
	return "test failed"
}

// failedOnlyThroughSubtest reports whether t failed with nothing of its own
// to say, because a subtest failed.
func failedOnlyThroughSubtest(t *testResult, p *packageResult) bool {
	return t.action == "fail" && t.output.Len() == 0 && p.hasFailedSubtest(t.key)
}

// hasFailedSubtest reports whether a subtest of the named test failed. The
// set of failed parents is built when first asked and dropped by Add when a
// result changes.
func (p *packageResult) hasFailedSubtest(name string) bool {
	if p.failedParents == nil {
		p.failedParents = map[string]bool{}
		for _, t := range p.tests {
			if t.action != "fail" {
				continue
			}
			for n := t.key; strings.Contains(n, "/"); {
				n = n[:strings.LastIndex(n, "/")]
				p.failedParents[n] = true
			}
		}
	}
	return p.failedParents[name]
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
	var total packageCounts
	var elapsed float64
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
		} else {
			suite.TestCases = p.junitTestCases()
		}
		total.add(c)
		elapsed += p.elapsed
		all.Suites = append(all.Suites, suite)
	}
	all.Tests, all.Failures, all.Errors, all.Skipped = total.tests, total.failures, total.errors, total.skipped
	all.Time = junitTime(elapsed)
	return all
}

func (p *packageResult) junitTestCases() []junitTestCase {
	var cases []junitTestCase
	for _, t := range p.tests {
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
		cases = append(cases, tc)
	}
	return cases
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
	var buf bytes.Buffer
	err := r.WriteJUnit(&buf)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o755)
	}
	if err == nil {
		err = os.WriteFile(path, buf.Bytes(), 0o644)
	}
	if err != nil {
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
		c := p.counts()
		total.add(c)
		// A package that never finished (an interrupted run) has
		// unfinished tests counted as failures, and is no "ok" either.
		status := "ok  "
		if p.action == "fail" || c.failures+c.errors > 0 {
			status = "FAIL"
		}
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
			if t.action == "pass" || t.action == "skip" {
				continue
			}
			// Every failure the counts include is listed, a parent that
			// failed through a subtest too ("a subtest failed").
			msg := failureMessage(t, p)
			entry := fmt.Sprintf("  %s: %s\n      %s\n", p.name, t.name, msg)
			if msg == packageFailedMessage && p.output.Len() > 0 {
				// No error or panic line to quote: show what it printed.
				entry += indent(p.output.String(), "      ")
			}
			failures = append(failures, entry)
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
// as is. It reads r to the end even after a read error, so the process
// writing it never blocks on a full pipe.
func consumeTestEvents(r io.Reader, report *TestReport, pr *testEventPrinter) error {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimRight(line, "\r\n")
			var ev TestEvent
			if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &ev) != nil {
				fmt.Fprintln(pr.stdout, string(line))
			} else {
				report.Add(ev)
				pr.print(ev)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			_, _ = io.Copy(io.Discard, br)
			return err
		}
	}
}
