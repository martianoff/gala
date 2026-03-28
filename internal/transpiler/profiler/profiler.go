// Package profiler provides lightweight compilation profiling for the GALA transpiler.
// Enable with GALA_PROFILE=1 environment variable.
package profiler

import (
	"fmt"
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
	mu     sync.Mutex
	events []event
	start  time.Time
	file   string
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

// Report prints the profiling summary to stderr.
// Safe to call on nil Profiler.
func (p *Profiler) Report() {
	if p == nil {
		return
	}
	total := time.Since(p.start)

	p.mu.Lock()
	defer p.mu.Unlock()

	fmt.Fprintf(os.Stderr, "\n=== GALA PROFILE: %s (total: %s) ===\n", p.file, total)

	// Find max label width for alignment
	maxWidth := 0
	for _, e := range p.events {
		if len(e.label) > maxWidth {
			maxWidth = len(e.label)
		}
	}

	for _, e := range p.events {
		pct := float64(e.duration) / float64(total) * 100
		bar := strings.Repeat("█", int(pct/2))
		fmt.Fprintf(os.Stderr, "  %-*s  %8s  %5.1f%%  %s\n",
			maxWidth, e.label, e.duration.Round(time.Millisecond), pct, bar)
	}
	fmt.Fprintln(os.Stderr)
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
	p.mu.Lock()
	defer p.mu.Unlock()

	entry := summaryEntry{
		file:   p.file,
		total:  time.Since(p.start),
		phases: make(map[string]time.Duration),
	}
	for _, e := range p.events {
		entry.phases[e.label] = e.duration
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, entry)
}

// Report prints the batch summary to stderr.
func (s *Summary) Report() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	wallTime := time.Since(s.start)

	// Collect all phase names
	phaseSet := make(map[string]bool)
	for _, e := range s.entries {
		for phase := range e.phases {
			phaseSet[phase] = true
		}
	}
	var phases []string
	for phase := range phaseSet {
		phases = append(phases, phase)
	}
	sort.Strings(phases)

	// Aggregate
	totalCPU := time.Duration(0)
	phaseTotal := make(map[string]time.Duration)
	for _, e := range s.entries {
		totalCPU += e.total
		for phase, d := range e.phases {
			phaseTotal[phase] += d
		}
	}

	fmt.Fprintf(os.Stderr, "\n╔══ GALA BATCH PROFILE SUMMARY ══════════════════════════════════╗\n")
	fmt.Fprintf(os.Stderr, "║  Files: %d   Wall: %s   CPU: %s\n", len(s.entries), wallTime.Round(time.Millisecond), totalCPU.Round(time.Millisecond))
	fmt.Fprintf(os.Stderr, "╠═══════════════════════════════════════════════════════════════════╣\n")

	// Per-phase aggregate
	fmt.Fprintf(os.Stderr, "║  Phase totals (sum across all files):\n")
	for _, phase := range phases {
		d := phaseTotal[phase]
		pct := float64(d) / float64(totalCPU) * 100
		bar := strings.Repeat("█", int(pct/2))
		fmt.Fprintf(os.Stderr, "║    %-30s  %8s  %5.1f%%  %s\n",
			phase, d.Round(time.Millisecond), pct, bar)
	}

	// Slowest files
	sort.Slice(s.entries, func(i, j int) bool {
		return s.entries[i].total > s.entries[j].total
	})
	fmt.Fprintf(os.Stderr, "║\n║  Slowest files:\n")
	limit := 10
	if len(s.entries) < limit {
		limit = len(s.entries)
	}
	for i := 0; i < limit; i++ {
		e := s.entries[i]
		fmt.Fprintf(os.Stderr, "║    %8s  %s\n", e.total.Round(time.Millisecond), e.file)
	}

	fmt.Fprintf(os.Stderr, "╚═══════════════════════════════════════════════════════════════════╝\n\n")
}
