package profiler

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestSummaryAggregatesPhasesAtCompletion(t *testing.T) {
	start := time.Unix(100, 0)
	completed := start.Add(25 * time.Millisecond)
	p := &Profiler{
		start:     start,
		completed: completed,
		file:      "sample.gala",
		events: []event{
			{label: "parse", duration: 2 * time.Millisecond},
			{label: "parse", duration: 3 * time.Millisecond},
			{label: "generate", duration: 4 * time.Millisecond},
		},
	}
	summary := &Summary{start: start}
	summary.Add(p)

	if len(summary.entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(summary.entries))
	}
	entry := summary.entries[0]
	if entry.total != 25*time.Millisecond {
		t.Fatalf("got total %s, want 25ms", entry.total)
	}
	if entry.phases["parse"] != 5*time.Millisecond {
		t.Fatalf("got parse total %s, want 5ms", entry.phases["parse"])
	}
	if entry.phases["generate"] != 4*time.Millisecond {
		t.Fatalf("got generate total %s, want 4ms", entry.phases["generate"])
	}

	var output bytes.Buffer
	summary.ReportTo(&output)
	text := output.String()
	if !strings.Contains(text, "File wall sum: 25ms") {
		t.Fatalf("summary did not use the completed file wall time: %s", text)
	}
	if strings.Contains(text, "CPU:") {
		t.Fatalf("summary mislabeled file wall time as CPU: %s", text)
	}
	if count := strings.Count(text, "parse"); count != 1 {
		t.Fatalf("got %d parse rows, want one aggregated row: %s", count, text)
	}
}

func TestSummaryReportOrdersEqualFilesByName(t *testing.T) {
	start := time.Unix(100, 0)
	completed := start.Add(10 * time.Millisecond)
	summary := &Summary{start: start}
	summary.Add(&Profiler{
		start:     start,
		completed: completed,
		file:      "z.gala",
		events:    []event{{label: "parse", duration: time.Millisecond}},
	})
	summary.Add(&Profiler{
		start:     start,
		completed: completed,
		file:      "a.gala",
		events:    []event{{label: "parse", duration: time.Millisecond}},
	})

	var output bytes.Buffer
	summary.ReportTo(&output)
	text := output.String()
	aIndex := strings.Index(text, "a.gala")
	zIndex := strings.Index(text, "z.gala")
	if aIndex < 0 || zIndex < 0 {
		t.Fatalf("summary omitted a file: %s", text)
	}
	if aIndex > zIndex {
		t.Fatalf("equal-duration files were not ordered by name: %s", text)
	}
}

func TestReportToSnapshotsCompletion(t *testing.T) {
	p := &Profiler{start: time.Now(), file: "snapshot.gala"}
	var output bytes.Buffer
	p.ReportTo(&output)
	completed := p.completed
	if completed.IsZero() {
		t.Fatal("ReportTo did not snapshot completion")
	}
	time.Sleep(2 * time.Millisecond)
	p.ReportTo(&output)
	if !p.completed.Equal(completed) {
		t.Fatalf("completion changed after reporting: got %s, want %s", p.completed, completed)
	}
}

func TestProfilerReportTo(t *testing.T) {
	start := time.Unix(100, 0)
	p := &Profiler{
		start:     start,
		completed: start.Add(4 * time.Millisecond),
		file:      "report.gala",
		events:    []event{{label: "parse", duration: 2 * time.Millisecond}},
	}

	var output bytes.Buffer
	p.ReportTo(&output)
	text := output.String()
	if !strings.Contains(text, "report.gala") {
		t.Fatalf("report omitted the file name: %s", text)
	}
	if !strings.Contains(text, "parse") || !strings.Contains(text, "2ms") {
		t.Fatalf("report omitted the phase timing: %s", text)
	}
}
