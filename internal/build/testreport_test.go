package build

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eventStream builds a test2json stream, one JSON event per line.
func eventStream(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

// readReport feeds a test2json stream through the same path `gala test`
// uses and returns the report with what was printed.
func readReport(t *testing.T, stream string, verbose bool) (report *TestReport, stdout, stderr string) {
	t.Helper()
	report = NewTestReport(func(pkg string) string {
		return strings.Replace(pkg, WorkspaceModule+"/gen", "example.com/proj", 1)
	})
	var out, errOut bytes.Buffer
	pr := &testEventPrinter{stdout: &out, stderr: &errOut, stream: verbose}
	require.NoError(t, consumeTestEvents(strings.NewReader(stream), report, pr))
	return report, out.String(), errOut.String()
}

// decodeJUnit writes the report as JUnit XML and parses it back, so each
// test checks the document a CI system would read.
func decodeJUnit(t *testing.T, report *TestReport) junitTestSuites {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, report.WriteJUnit(&buf))
	require.True(t, strings.HasPrefix(buf.String(), xml.Header))
	var doc junitTestSuites
	require.NoError(t, xml.Unmarshal(buf.Bytes(), &doc), buf.String())
	return doc
}

type wantCase struct {
	name    string
	outcome string // pass, fail, skip or error
	message string // the failure, error or skip message
	body    string // a substring of the failure or error text
}

func outcomeOf(tc junitTestCase) (outcome, message, body string) {
	switch {
	case tc.Failure != nil:
		return "fail", tc.Failure.Message, tc.Failure.Body
	case tc.Error != nil:
		return "error", tc.Error.Message, tc.Error.Body
	case tc.Skipped != nil:
		return "skip", tc.Skipped.Message, tc.Skipped.Body
	}
	return "pass", "", tc.SystemOut
}

const pkgMain = `"Package":"gala-build-workspace/gen"`

func TestTestReport_JUnit(t *testing.T) {
	tests := []struct {
		name   string
		stream string
		// suite is the expected test cases of the single package reported.
		suite   []wantCase
		counts  [4]int // tests, failures, errors, skipped
		sysOut  string // a substring of the suite's system-out
		elapsed string
	}{
		{
			name: "passing, failing and skipped tests",
			stream: eventStream(
				`{"Action":"start",`+pkgMain+`}`,
				`{"Action":"output",`+pkgMain+`,"Output":"=== STARTING TESTS ===\n"}`,
				`{"Action":"run",`+pkgMain+`,"Test":"TestAdd"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestAdd","Output":"=== RUN   TestAdd\n"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestAdd","Output":"--- PASS: TestAdd (0.012s)\n"}`,
				`{"Action":"pass",`+pkgMain+`,"Test":"TestAdd","Elapsed":0.012}`,
				`{"Action":"run",`+pkgMain+`,"Test":"TestBad"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestBad","Output":"=== RUN   TestBad\n"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestBad","Output":"    ERROR: expected 4, got 3\n"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestBad","Output":"--- FAIL: TestBad (0.000s)\n"}`,
				`{"Action":"fail",`+pkgMain+`,"Test":"TestBad","Elapsed":0}`,
				`{"Action":"run",`+pkgMain+`,"Test":"TestSkip"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestSkip","Output":"=== RUN   TestSkip\n"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestSkip","Output":"--- SKIP: TestSkip (0.000s)\n"}`,
				// test2json leaves the runner's closing lines with the last test.
				`{"Action":"output",`+pkgMain+`,"Test":"TestSkip","Output":"=== RESULTS ===\n"}`,
				`{"Action":"skip",`+pkgMain+`,"Test":"TestSkip","Elapsed":0}`,
				`{"Action":"output",`+pkgMain+`,"Output":"FAIL\n"}`,
				`{"Action":"fail",`+pkgMain+`,"Elapsed":0.25}`,
			),
			suite: []wantCase{
				{name: "TestAdd", outcome: "pass"},
				{name: "TestBad", outcome: "fail", message: "expected 4, got 3", body: "ERROR: expected 4, got 3"},
				{name: "TestSkip", outcome: "skip", message: "skipped"},
			},
			counts:  [4]int{3, 1, 0, 1},
			sysOut:  "=== RESULTS ===",
			elapsed: "0.250",
		},
		{
			name: "a recovered panic and one that kills the test binary",
			stream: eventStream(
				`{"Action":"run",`+pkgMain+`,"Test":"TestRecovered"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestRecovered","Output":"    ERROR: PANIC: runtime error: integer divide by zero\n"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestRecovered","Output":"--- FAIL: TestRecovered (0.001s)\n"}`,
				`{"Action":"fail",`+pkgMain+`,"Test":"TestRecovered","Elapsed":0.001}`,
				`{"Action":"run",`+pkgMain+`,"Test":"TestCrash"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestCrash","Output":"=== RUN   TestCrash\n"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestCrash","Output":"panic: boom\n"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestCrash","Output":"\n"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestCrash","Output":"goroutine 1 [running]:\n"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestCrash","Output":"main.TestCrash(...)\n\t/src/proj/crash_test.gala:7 +0x1d\n"}`,
				`{"Action":"fail",`+pkgMain+`,"Elapsed":0.3}`,
			),
			suite: []wantCase{
				{name: "TestRecovered", outcome: "fail", message: "PANIC: runtime error: integer divide by zero"},
				{name: "TestCrash", outcome: "fail", message: "panic: boom", body: "crash_test.gala:7"},
			},
			counts: [4]int{2, 2, 0, 0},
		},
		{
			name: "a package that does not build is an error, not dropped",
			stream: eventStream(
				`{"ImportPath":"gala-build-workspace/gen [gala-build-workspace/gen.test]","Action":"build-output","Output":"# gala-build-workspace/gen [gala-build-workspace/gen.test]\n"}`,
				`{"ImportPath":"gala-build-workspace/gen [gala-build-workspace/gen.test]","Action":"build-output","Output":"/src/proj/lib.gala:7: cannot use \"x\" (untyped string constant) as int value\n"}`,
				`{"ImportPath":"gala-build-workspace/gen [gala-build-workspace/gen.test]","Action":"build-fail"}`,
				`{"Action":"start",`+pkgMain+`}`,
				`{"Action":"output",`+pkgMain+`,"Output":"FAIL\tgala-build-workspace/gen [build failed]\n"}`,
				`{"Action":"fail",`+pkgMain+`,"Elapsed":0,"FailedBuild":"gala-build-workspace/gen [gala-build-workspace/gen.test]"}`,
			),
			suite: []wantCase{
				{name: "[build failed]", outcome: "error", message: "build failed", body: "lib.gala:7: cannot use"},
			},
			counts: [4]int{1, 0, 1, 0},
		},
		{
			name: "nested subtests keep their full names",
			stream: eventStream(
				`{"Action":"run",`+pkgMain+`,"Test":"TestSubs"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestSubs","Output":"=== RUN   TestSubs\n"}`,
				`{"Action":"run",`+pkgMain+`,"Test":"TestSubs/one"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestSubs/one","Output":"--- PASS: TestSubs/one (0.000s)\n"}`,
				`{"Action":"pass",`+pkgMain+`,"Test":"TestSubs/one","Elapsed":0}`,
				`{"Action":"run",`+pkgMain+`,"Test":"TestSubs/two"}`,
				`{"Action":"run",`+pkgMain+`,"Test":"TestSubs/two/deep"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestSubs/two/deep","Output":"    ERROR: expected 2, got 1\n"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestSubs/two/deep","Output":"--- FAIL: TestSubs/two/deep (0.000s)\n"}`,
				`{"Action":"fail",`+pkgMain+`,"Test":"TestSubs/two/deep","Elapsed":0}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestSubs/two","Output":"--- FAIL: TestSubs/two (0.000s)\n"}`,
				`{"Action":"fail",`+pkgMain+`,"Test":"TestSubs/two","Elapsed":0}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestSubs","Output":"--- FAIL: TestSubs (0.001s)\n"}`,
				`{"Action":"fail",`+pkgMain+`,"Test":"TestSubs","Elapsed":0.001}`,
				`{"Action":"fail",`+pkgMain+`,"Elapsed":0.1}`,
			),
			suite: []wantCase{
				{name: "TestSubs", outcome: "fail", message: "a subtest failed"},
				{name: "TestSubs/one", outcome: "pass"},
				{name: "TestSubs/two", outcome: "fail", message: "a subtest failed"},
				{name: "TestSubs/two/deep", outcome: "fail", message: "expected 2, got 1"},
			},
			counts: [4]int{4, 3, 0, 0},
		},
		{
			// GALA's framework prints a subtest's result before its parent
			// goes on, so test2json files the parent's later lines under
			// the subtest that already reported.
			name: "what a parent logs after a subtest finished stays with the parent",
			stream: eventStream(
				`{"Action":"run",`+pkgMain+`,"Test":"TestP"}`,
				`{"Action":"run",`+pkgMain+`,"Test":"TestP/a"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestP/a","Output":"--- PASS: TestP/a (0.000s)\n"}`,
				`{"Action":"pass",`+pkgMain+`,"Test":"TestP/a","Elapsed":0}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestP/a","Output":"    ERROR: bad total\n"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestP","Output":"--- FAIL: TestP (0.000s)\n"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestP","Output":"=== RESULTS ===\n"}`,
				`{"Action":"fail",`+pkgMain+`,"Test":"TestP","Elapsed":0}`,
				`{"Action":"fail",`+pkgMain+`,"Elapsed":0.1}`,
			),
			suite: []wantCase{
				{name: "TestP", outcome: "fail", message: "bad total", body: "ERROR: bad total"},
				{name: "TestP/a", outcome: "pass"},
			},
			counts: [4]int{2, 1, 0, 0},
			sysOut: "=== RESULTS ===",
		},
		{
			name: "parallel tests with interleaved events",
			stream: eventStream(
				`{"Action":"run",`+pkgMain+`,"Test":"TestA"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestA","Output":"=== RUN   TestA\n"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestA","Output":"=== PAUSE TestA\n"}`,
				`{"Action":"pause",`+pkgMain+`,"Test":"TestA"}`,
				`{"Action":"run",`+pkgMain+`,"Test":"TestB"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestB","Output":"=== RUN   TestB\n"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestB","Output":"=== PAUSE TestB\n"}`,
				`{"Action":"pause",`+pkgMain+`,"Test":"TestB"}`,
				`{"Action":"cont",`+pkgMain+`,"Test":"TestA"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestA","Output":"=== CONT  TestA\n"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestA","Output":"    log from A\n"}`,
				`{"Action":"cont",`+pkgMain+`,"Test":"TestB"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestB","Output":"    log from B\n"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestA","Output":"    ERROR: A broke\n"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestB","Output":"--- PASS: TestB (0.020s)\n"}`,
				`{"Action":"pass",`+pkgMain+`,"Test":"TestB","Elapsed":0.02}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestA","Output":"--- FAIL: TestA (0.030s)\n"}`,
				`{"Action":"fail",`+pkgMain+`,"Test":"TestA","Elapsed":0.03}`,
				`{"Action":"fail",`+pkgMain+`,"Elapsed":0.05}`,
			),
			suite: []wantCase{
				{name: "TestA", outcome: "fail", message: "A broke", body: "    log from A\n    ERROR: A broke\n"},
				{name: "TestB", outcome: "pass", body: "    log from B\n"},
			},
			counts: [4]int{2, 1, 0, 0},
		},
		{
			name: "a package that fails outside its tests",
			stream: eventStream(
				`{"Action":"run",`+pkgMain+`,"Test":"TestOK"}`,
				`{"Action":"output",`+pkgMain+`,"Test":"TestOK","Output":"--- PASS: TestOK (0.000s)\n"}`,
				`{"Action":"pass",`+pkgMain+`,"Test":"TestOK","Elapsed":0}`,
				`{"Action":"output",`+pkgMain+`,"Output":"exit status 3\n"}`,
				`{"Action":"fail",`+pkgMain+`,"Elapsed":0.1}`,
			),
			suite: []wantCase{
				{name: "TestOK", outcome: "pass"},
				{name: "[package failed]", outcome: "error", message: "the test binary failed outside of any test", body: "exit status 3"},
			},
			counts: [4]int{2, 0, 1, 0},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, _, _ := readReport(t, tc.stream, false)
			doc := decodeJUnit(t, report)
			require.Len(t, doc.Suites, 1)
			suite := doc.Suites[0]
			assert.Equal(t, "example.com/proj", suite.Name)
			assert.Equal(t, tc.counts, [4]int{suite.Tests, suite.Failures, suite.Errors, suite.Skipped}, "tests, failures, errors, skipped")
			assert.Equal(t, tc.counts, [4]int{doc.Tests, doc.Failures, doc.Errors, doc.Skipped}, "totals")
			if tc.elapsed != "" {
				assert.Equal(t, tc.elapsed, suite.Time)
			}
			if tc.sysOut != "" {
				assert.Contains(t, suite.SystemOut, tc.sysOut)
			}
			require.Len(t, suite.TestCases, len(tc.suite))
			for i, want := range tc.suite {
				got := suite.TestCases[i]
				assert.Equal(t, want.name, got.Name)
				assert.Equal(t, "example.com/proj", got.Classname)
				outcome, message, body := outcomeOf(got)
				assert.Equal(t, want.outcome, outcome, want.name)
				assert.Equal(t, want.message, message, want.name)
				assert.Contains(t, body, want.body, want.name)
				// The runner's own framing lines are not the test's output.
				assert.NotContains(t, body, "=== RUN", want.name)
				assert.NotContains(t, body, "--- ", want.name)
			}
		})
	}
}

func TestTestReport_SkipsPackagesWithoutTests(t *testing.T) {
	report, _, _ := readReport(t, eventStream(
		`{"Action":"start","Package":"gala-build-workspace/gen/notests"}`,
		`{"Action":"output","Package":"gala-build-workspace/gen/notests","Output":"?   \tgala-build-workspace/gen/notests\t[no test files]\n"}`,
		`{"Action":"skip","Package":"gala-build-workspace/gen/notests","Elapsed":0}`,
	), false)
	assert.True(t, report.Empty())
	assert.Empty(t, decodeJUnit(t, report).Suites)
}

func TestTestReport_SortsSuitesAndKeepsXMLValid(t *testing.T) {
	report, _, _ := readReport(t, eventStream(
		`{"Action":"run","Package":"gala-build-workspace/gen/zeta","Test":"TestZ"}`,
		`{"Action":"pass","Package":"gala-build-workspace/gen/zeta","Test":"TestZ","Elapsed":0.5}`,
		`{"Action":"pass","Package":"gala-build-workspace/gen/zeta","Elapsed":0.5}`,
		`{"Action":"run",`+pkgMain+`,"Test":"TestColor"}`,
		// A terminal escape and markup characters, neither valid raw in XML.
		`{"Action":"output",`+pkgMain+`,"Test":"TestColor","Output":"    ERROR: \u001b[31m<red> & \"quoted\"\u001b[0m\n"}`,
		`{"Action":"fail",`+pkgMain+`,"Test":"TestColor","Elapsed":0}`,
		`{"Action":"fail",`+pkgMain+`,"Elapsed":0.25}`,
	), false)
	doc := decodeJUnit(t, report)
	require.Len(t, doc.Suites, 2)
	assert.Equal(t, "example.com/proj", doc.Suites[0].Name)
	assert.Equal(t, "example.com/proj/zeta", doc.Suites[1].Name)
	assert.Equal(t, "0.750", doc.Time)
	assert.Contains(t, doc.Suites[0].TestCases[0].Failure.Message, `<red> & "quoted"`)
}

func TestTestReport_BuildFailureBeforeAnyEvent(t *testing.T) {
	report := NewTestReport(nil)
	report.AddBuildFailure("example.com/proj", "error[GALA-E0023]: undefined: Array")
	doc := decodeJUnit(t, report)
	require.Len(t, doc.Suites, 1)
	assert.Equal(t, 1, doc.Errors)
	tc := doc.Suites[0].TestCases[0]
	assert.Equal(t, "[build failed]", tc.Name)
	require.NotNil(t, tc.Error)
	assert.Contains(t, tc.Error.Body, "undefined: Array")
}

func TestTestReport_Summary(t *testing.T) {
	report, _, _ := readReport(t, eventStream(
		`{"Action":"run",`+pkgMain+`,"Test":"TestAdd"}`,
		`{"Action":"pass",`+pkgMain+`,"Test":"TestAdd","Elapsed":0}`,
		`{"Action":"run",`+pkgMain+`,"Test":"TestBad"}`,
		`{"Action":"output",`+pkgMain+`,"Test":"TestBad","Output":"    ERROR: expected 4, got 3\n"}`,
		`{"Action":"fail",`+pkgMain+`,"Test":"TestBad","Elapsed":0}`,
		`{"Action":"run",`+pkgMain+`,"Test":"TestSubs"}`,
		`{"Action":"run",`+pkgMain+`,"Test":"TestSubs/two"}`,
		`{"Action":"output",`+pkgMain+`,"Test":"TestSubs/two","Output":"    ERROR: expected 2, got 1\n"}`,
		`{"Action":"fail",`+pkgMain+`,"Test":"TestSubs/two","Elapsed":0}`,
		`{"Action":"fail",`+pkgMain+`,"Test":"TestSubs","Elapsed":0}`,
		`{"Action":"run",`+pkgMain+`,"Test":"TestSkip"}`,
		`{"Action":"skip",`+pkgMain+`,"Test":"TestSkip","Elapsed":0}`,
		`{"Action":"fail",`+pkgMain+`,"Elapsed":0.1}`,
		`{"Action":"run","Package":"gala-build-workspace/gen/sub","Test":"TestMul"}`,
		`{"Action":"pass","Package":"gala-build-workspace/gen/sub","Test":"TestMul","Elapsed":0}`,
		`{"Action":"pass","Package":"gala-build-workspace/gen/sub","Elapsed":0.1}`,
		`{"ImportPath":"gala-build-workspace/gen/broken [gala-build-workspace/gen/broken.test]","Action":"build-output","Output":"broken.gala:3: undefined: x\n"}`,
		`{"Action":"fail","Package":"gala-build-workspace/gen/broken","Elapsed":0,"FailedBuild":"gala-build-workspace/gen/broken [gala-build-workspace/gen/broken.test]"}`,
	), false)
	var buf bytes.Buffer
	report.WriteSummary(&buf)
	assert.Equal(t, `
=== SUMMARY ===
FAIL  example.com/proj         1 passed, 3 failed, 1 skipped
FAIL  example.com/proj/broken  build failed
ok    example.com/proj/sub     1 passed, 0 failed, 0 skipped

Failures:
  example.com/proj: TestBad
      expected 4, got 3
  example.com/proj: TestSubs
      a subtest failed
  example.com/proj: TestSubs/two
      expected 2, got 1
  example.com/proj/broken: build failed
      broken.gala:3: undefined: x

2 passed, 4 failed, 1 skipped in 3 package(s)
`, buf.String())
}

func TestTestReport_SummaryOfUnfinishedAndCrashedPackages(t *testing.T) {
	report, _, _ := readReport(t, eventStream(
		// Interrupted: the package never reported its own result.
		`{"Action":"run","Package":"gala-build-workspace/gen/cut","Test":"TestSlow"}`,
		// Failed outside its tests.
		`{"Action":"output",`+pkgMain+`,"Output":"panic: init failed\n"}`,
		`{"Action":"fail",`+pkgMain+`,"Elapsed":0.1}`,
	), false)
	var buf bytes.Buffer
	report.WriteSummary(&buf)
	assert.Contains(t, buf.String(), "FAIL  example.com/proj/cut  0 passed, 1 failed, 0 skipped\n")
	assert.Contains(t, buf.String(), "  example.com/proj/cut: TestSlow\n      the test did not finish\n")
	// The panic is the reason, not the generic message.
	assert.Contains(t, buf.String(), "  example.com/proj: [package failed]\n      panic: init failed\n")
	assert.NotContains(t, buf.String(), packageFailedMessage)
}

// Two t.Run calls with the same name are two test cases: a failing second
// run must not hide behind a passing first one.
func TestTestReport_RepeatedSubtestNames(t *testing.T) {
	report, _, _ := readReport(t, eventStream(
		`{"Action":"run",`+pkgMain+`,"Test":"TestT"}`,
		`{"Action":"run",`+pkgMain+`,"Test":"TestT/case"}`,
		`{"Action":"output",`+pkgMain+`,"Test":"TestT/case","Output":"=== RUN   TestT/case\n"}`,
		`{"Action":"output",`+pkgMain+`,"Test":"TestT/case","Output":"--- PASS: TestT/case (0.000s)\n"}`,
		`{"Action":"pass",`+pkgMain+`,"Test":"TestT/case","Elapsed":0}`,
		`{"Action":"run",`+pkgMain+`,"Test":"TestT/case"}`,
		`{"Action":"output",`+pkgMain+`,"Test":"TestT/case","Output":"=== RUN   TestT/case\n"}`,
		`{"Action":"output",`+pkgMain+`,"Test":"TestT/case","Output":"    ERROR: expected 2, got 1\n"}`,
		`{"Action":"output",`+pkgMain+`,"Test":"TestT/case","Output":"--- FAIL: TestT/case (0.000s)\n"}`,
		`{"Action":"fail",`+pkgMain+`,"Test":"TestT/case","Elapsed":0}`,
		`{"Action":"output",`+pkgMain+`,"Test":"TestT","Output":"--- FAIL: TestT (0.000s)\n"}`,
		`{"Action":"fail",`+pkgMain+`,"Test":"TestT","Elapsed":0}`,
		`{"Action":"fail",`+pkgMain+`,"Elapsed":0.1}`,
	), false)
	cases := decodeJUnit(t, report).Suites[0].TestCases
	require.Len(t, cases, 3)
	assert.Equal(t, "TestT/case", cases[1].Name)
	assert.Nil(t, cases[1].Failure)
	assert.Equal(t, "TestT/case#01", cases[2].Name)
	require.NotNil(t, cases[2].Failure)
	assert.Equal(t, "expected 2, got 1", cases[2].Failure.Message)
}

// A test that logs a line looking like the runner's framing keeps it: only
// the framework's own unindented lines naming the test are framing.
func TestTestReport_LoggedLinesThatLookLikeFraming(t *testing.T) {
	report, _, _ := readReport(t, eventStream(
		`{"Action":"run",`+pkgMain+`,"Test":"TestLog"}`,
		`{"Action":"output",`+pkgMain+`,"Test":"TestLog","Output":"=== RUN   TestLog\n"}`,
		`{"Action":"output",`+pkgMain+`,"Test":"TestLog","Output":"    --- FAIL: expected header\n"}`,
		`{"Action":"output",`+pkgMain+`,"Test":"TestLog","Output":"    === RUN   TestLog\n"}`,
		`{"Action":"output",`+pkgMain+`,"Test":"TestLog","Output":"    ERROR: mismatch\n"}`,
		`{"Action":"output",`+pkgMain+`,"Test":"TestLog","Output":"--- FAIL: TestLog (0.000s)\n"}`,
		`{"Action":"fail",`+pkgMain+`,"Test":"TestLog","Elapsed":0}`,
		`{"Action":"fail",`+pkgMain+`,"Elapsed":0.1}`,
	), false)
	tc := decodeJUnit(t, report).Suites[0].TestCases[0]
	require.NotNil(t, tc.Failure)
	assert.Equal(t, "    --- FAIL: expected header\n    === RUN   TestLog\n    ERROR: mismatch\n", tc.Failure.Body)
}

// An error that reaches the end of a run without being in the report goes
// in before the summary is printed, so the summary does not say "ok" for a
// run that fails.
func TestFinishTestRun_LateErrorIsInTheSummary(t *testing.T) {
	report, _, _ := readReport(t, eventStream(
		`{"Action":"run",`+pkgMain+`,"Test":"TestOK"}`,
		`{"Action":"pass",`+pkgMain+`,"Test":"TestOK","Elapsed":0}`,
		`{"Action":"pass",`+pkgMain+`,"Elapsed":0.1}`,
	), false)
	late := errors.New("reading test events: broken pipe")
	var out bytes.Buffer
	junit := filepath.Join(t.TempDir(), "junit.xml")
	err := finishTestRun(report, errors.Join(reportedError{errors.New("tests failed (exit code 1)")}, late), junit, "example.com/proj", &out)
	require.Error(t, err)
	assert.Contains(t, out.String(), "FAIL  gala test         build failed\n")
	assert.Contains(t, out.String(), "reading test events: broken pipe")

	data, readErr := os.ReadFile(junit)
	require.NoError(t, readErr)
	var doc junitTestSuites
	require.NoError(t, xml.Unmarshal(data, &doc))
	assert.Equal(t, 1, doc.Errors)
}

// A run that fails before any package ran (a GALA compile error) prints no
// summary, but its report still shows the module's failed build.
func TestFinishTestRun_EarlyError(t *testing.T) {
	var out bytes.Buffer
	junit := filepath.Join(t.TempDir(), "junit.xml")
	err := finishTestRun(NewTestReport(nil), errors.New("undefined: nope"), junit, "example.com/proj", &out)
	require.Error(t, err)
	assert.Empty(t, out.String())
	data, readErr := os.ReadFile(junit)
	require.NoError(t, readErr)
	assert.Contains(t, string(data), `<testsuite name="example.com/proj" tests="1" failures="0" errors="1"`)
}

func TestConsumeTestEvents_LongLine(t *testing.T) {
	long := strings.Repeat("x", 200*1024)
	report, _, _ := readReport(t, eventStream(
		`{"Action":"run",`+pkgMain+`,"Test":"TestBig"}`,
		`{"Action":"output",`+pkgMain+`,"Test":"TestBig","Output":"`+long+`\n"}`,
		`{"Action":"pass",`+pkgMain+`,"Test":"TestBig","Elapsed":0}`,
		`{"Action":"pass",`+pkgMain+`,"Elapsed":0.1}`,
	), false)
	doc := decodeJUnit(t, report)
	require.Len(t, doc.Suites, 1)
	assert.Equal(t, long+"\n", doc.Suites[0].TestCases[0].SystemOut)
}

func TestTestEventPrinter(t *testing.T) {
	stream := eventStream(
		`{"ImportPath":"gala-build-workspace/gen/broken [gala-build-workspace/gen/broken.test]","Action":"build-output","Output":"broken.gala:3: undefined: x\n"}`,
		`{"Action":"output","Package":"gala-build-workspace/gen/sub","Output":"=== STARTING TESTS ===\n"}`,
		`{"Action":"output","Package":"gala-build-workspace/gen/sub","Output":"PASS\n"}`,
		`{"Action":"output","Package":"gala-build-workspace/gen/sub","Output":"ok  \tgala-build-workspace/gen/sub\t0.074s\n"}`,
		`{"Action":"pass","Package":"gala-build-workspace/gen/sub","Elapsed":0.074}`,
		`{"Action":"output",`+pkgMain+`,"Test":"TestBad","Output":"    ERROR: expected 4, got 3\n"}`,
		`{"Action":"output",`+pkgMain+`,"Output":"FAIL\tgala-build-workspace/gen\t0.067s\n"}`,
		`{"Action":"fail",`+pkgMain+`,"Elapsed":0.067}`,
		`not an event`,
	)
	t.Run("plain go test prints a passed package's ok line and a failed package's output", func(t *testing.T) {
		_, stdout, stderr := readReport(t, stream, false)
		assert.Equal(t, "ok  \tgala-build-workspace/gen/sub\t0.074s\n"+
			"    ERROR: expected 4, got 3\nFAIL\tgala-build-workspace/gen\t0.067s\n"+
			"not an event\n", stdout)
		assert.Equal(t, "broken.gala:3: undefined: x\n", stderr)
	})
	t.Run("verbose prints everything as it comes", func(t *testing.T) {
		_, stdout, _ := readReport(t, stream, true)
		assert.Equal(t, "=== STARTING TESTS ===\nPASS\nok  \tgala-build-workspace/gen/sub\t0.074s\n"+
			"    ERROR: expected 4, got 3\nFAIL\tgala-build-workspace/gen\t0.067s\n"+
			"not an event\n", stdout)
	})
}

func TestWithoutReported(t *testing.T) {
	failed := reportedError{errors.New("tests failed (exit code 1)")}
	other := errors.New("reading test events: broken pipe")
	tests := []struct {
		name string
		err  error
		want string // "" for nil
	}{
		{name: "no error", err: nil},
		{name: "only a reported test failure", err: failed},
		{name: "a wrapped reported failure", err: fmt.Errorf("root: %w", failed)},
		{name: "an unreported error", err: other, want: other.Error()},
		{name: "a reported failure joined with an unreported error", err: errors.Join(failed, other), want: other.Error()},
		{name: "two reported failures", err: errors.Join(failed, failed)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := withoutReported(tc.err)
			if tc.want == "" {
				assert.NoError(t, got)
				return
			}
			require.Error(t, got)
			assert.Equal(t, tc.want, got.Error())
		})
	}
}
