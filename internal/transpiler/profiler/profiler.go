// Package profiler provides lightweight compilation profiling for the GALA transpiler.
// Enable with GALA_PROFILE=1 environment variable.
package profiler

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Enabled returns true when GALA_PROFILE=1 is set.
var Enabled = os.Getenv("GALA_PROFILE") == "1"

// event records a single timed span.
type event struct {
	label    string
	duration time.Duration
}

// Profiler collects timing events for a single transpilation run.
type Profiler struct {
	mu        sync.Mutex
	events    []event
	start     time.Time
	completed time.Time
	file      string
}

// New creates a Profiler for the given file. Returns nil if profiling is disabled.
func New(file string) *Profiler {
	if !Enabled {
		return nil
	}
	return &Profiler{
		start: time.Now(),
		file:  file,
	}
}

// Phase records the start of a named phase and returns a function to call when done.
// Safe to call on nil Profiler.
func (p *Profiler) Phase(label string) func() {
	if p == nil {
		return func() {}
	}
	start := time.Now()
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.events = append(p.events, event{label: label, duration: time.Since(start)})
	}
}

func (p *Profiler) snapshot() (time.Duration, []event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.completed.IsZero() {
		p.completed = time.Now()
	}
	events := append([]event(nil), p.events...)
	return p.completed.Sub(p.start), events
}

// Report prints the profiling summary to stderr.
// Safe to call on nil Profiler.
func (p *Profiler) Report() {
	p.ReportTo(os.Stderr)
}

func (p *Profiler) ReportTo(w io.Writer) {
	if p == nil || w == nil {
		return
	}
	total, events := p.snapshot()

	fmt.Fprintf(w, "\n=== GALA PROFILE: %s (total: %s) ===\n", p.file, total)

	// Find max label width for alignment
	maxWidth := 0
	for _, e := range events {
		if len(e.label) > maxWidth {
			maxWidth = len(e.label)
		}
	}

	for _, e := range events {
		pct := 0.0
		if total > 0 {
			pct = float64(e.duration) / float64(total) * 100
		}
		bar := strings.Repeat("█", int(pct/2))
		fmt.Fprintf(w, "  %-*s  %8s  %5.1f%%  %s\n",
			maxWidth, e.label, e.duration.Round(time.Millisecond), pct, bar)
	}
	fmt.Fprintln(w)
}

// Summary is a top-level summary across all files in a batch.
type Summary struct {
	mu      sync.Mutex
	entries []summaryEntry
	start   time.Time
}

type summaryEntry struct {
	file  string
	total time.Duration
	// breakdown by phase
	phases map[string]time.Duration
}

// NewSummary creates a batch summary. Returns nil if profiling is disabled.
func NewSummary() *Summary {
	if !Enabled {
		return nil
	}
	return &Summary{start: time.Now()}
}

// Add records a file's profiling result into the summary.
func (s *Summary) Add(p *Profiler) {
	if s == nil || p == nil {
		return
	}
	total, events := p.snapshot()
	entry := summaryEntry{
		file:   p.file,
		total:  total,
		phases: make(map[string]time.Duration),
	}
	for _, e := range events {
		entry.phases[e.label] += e.duration
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, entry)
}

// Report prints the batch summary to stderr.
func (s *Summary) Report() {
	s.ReportTo(os.Stderr)
}

func (s *Summary) ReportTo(w io.Writer) {
	if s == nil || w == nil {
		return
	}

	s.mu.Lock()
	entries := make([]summaryEntry, len(s.entries))
	for i, entry := range s.entries {
		entries[i] = summaryEntry{
			file:   entry.file,
			total:  entry.total,
			phases: make(map[string]time.Duration, len(entry.phases)),
		}
		for phase, duration := range entry.phases {
			entries[i].phases[phase] = duration
		}
	}
	start := s.start
	s.mu.Unlock()

	wallTime := time.Since(start)

	// Collect all phase names
	phaseSet := make(map[string]struct{})
	for _, entry := range entries {
		for phase := range entry.phases {
			phaseSet[phase] = struct{}{}
		}
	}
	phases := make([]string, 0, len(phaseSet))
	for phase := range phaseSet {
		phases = append(phases, phase)
	}
	sort.Strings(phases)

	// Aggregate
	totalFileWall := time.Duration(0)
	phaseTotal := make(map[string]time.Duration)
	for _, entry := range entries {
		totalFileWall += entry.total
		for phase, duration := range entry.phases {
			phaseTotal[phase] += duration
		}
	}

	fmt.Fprintf(w, "\n╔══ GALA BATCH PROFILE SUMMARY ══════════════════════════════════╗\n")
	fmt.Fprintf(w, "║  Files: %d   Wall: %s   File wall sum: %s\n", len(entries), wallTime.Round(time.Millisecond), totalFileWall.Round(time.Millisecond))
	fmt.Fprintf(w, "╠═══════════════════════════════════════════════════════════════════╣\n")

	// Per-phase aggregate
	fmt.Fprintf(w, "║  Phase totals (sum across all files):\n")
	for _, phase := range phases {
		duration := phaseTotal[phase]
		pct := 0.0
		if totalFileWall > 0 {
			pct = float64(duration) / float64(totalFileWall) * 100
		}
		bar := strings.Repeat("█", int(pct/2))
		fmt.Fprintf(w, "║    %-30s  %8s  %5.1f%%  %s\n",
			phase, duration.Round(time.Millisecond), pct, bar)
	}

	// Slowest files
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].total != entries[j].total {
			return entries[i].total > entries[j].total
		}
		return entries[i].file < entries[j].file
	})
	fmt.Fprintf(w, "║\n║  Slowest files:\n")
	limit := 10
	if len(entries) < limit {
		limit = len(entries)
	}
	for i := 0; i < limit; i++ {
		entry := entries[i]
		fmt.Fprintf(w, "║    %8s  %s\n", entry.total.Round(time.Millisecond), entry.file)
	}

	fmt.Fprintf(w, "╚═══════════════════════════════════════════════════════════════════╝\n\n")
}
