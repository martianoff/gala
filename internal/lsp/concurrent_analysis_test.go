package lsp_test

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	lsp "github.com/owenrumney/go-lsp/lsp"
)

// concurrentDoc builds a self-contained GALA source whose declarations are
// named per seed, so several documents can be analysed at once without
// colliding, and whose bodies are heavy enough (match arms, generics, a lambda,
// an interpolated string) to keep ANTLR's adaptive prediction busy.
func concurrentDoc(seed, version int) string {
	return fmt.Sprintf(`package main

sealed type Signal%[1]d {
    case Ping%[1]d(Seq int)
    case Pong%[1]d
}

func classify%[1]d(sig Signal%[1]d) string = sig match {
    case Pong%[1]d => "pong"
    case Ping%[1]d(n) => s"ping $n"
}

func wrap%[1]d[T any](v T) T = v

func compute%[1]d(a int, b int) int = ((a + b) * %[2]d) - (a match {
    case 0 => %[1]d
    case _ => a
})

func main() {
    val fn = (val x int) => compute%[1]d(x, %[2]d)
    Println(s"r=${fn(%[2]d)} c=${classify%[1]d(Ping%[1]d(Seq = %[1]d))}")
    Println(wrap%[1]d[string]("done"))
}
`, seed, version)
}

// TestConcurrentAnalysesAcrossDocuments overlaps several document analyses and
// hammers read requests while they are in flight.
//
// This is the LSP's real concurrency shape, and nothing covered it. DidChange
// returns as soon as it has spawned `go h.analyzeFile(...)`, so editing N
// documents in quick succession leaves N analyses running at once — and each one
// fans out again through analyzer.parseFilesConcurrent. That makes the language
// server the heaviest concurrent consumer of the shared ANTLR parser in the
// codebase, heavier than any CLI transpile, while the synchronous requests
// interleaved here read the very maps (richASTs, parseTrees, documents) those
// background analyses write.
//
// The handler is safe by inspection — GalaHandler splits set-once configuration
// above its mutex from guarded state below, and every map access is inside the
// lock — but inspection is what this codebase already relied on for the parser's
// prediction-cache isolation, which had been asserted backwards in a comment for
// months. So this test exists to be run under `-race` by the `race` job, where
// a dropped lock fails loudly instead of corrupting an editor session.
//
// Assertions are deliberately weak: analyses race each other by construction and
// a later edit is allowed to supersede an earlier one, so pinning diagnostics
// would make this flaky. What must hold is that the server stays alive, answers,
// and reports no race.
func TestConcurrentAnalysesAcrossDocuments(t *testing.T) {
	h := newHarness(t)

	const docs = 6
	const rounds = 3

	uris := make([]lsp.DocumentURI, docs)
	for i := range uris {
		uris[i] = openNamedFileOnDisk(t, h, fmt.Sprintf("doc%d.gala", i), concurrentDoc(i, 1))
	}

	// Captured after the documents are open so the baseline excludes the
	// harness's own goroutines; the drain at the end compares against it.
	baseline := runtime.NumGoroutine()

	// Fire every edit without waiting in between: the point is to have several
	// analyzeFile goroutines alive at the same moment.
	// The document version doubles as the fixture's multiplier, so each round
	// really does produce different source rather than re-sending the same text.
	for version := 2; version <= rounds+1; version++ {
		for i, uri := range uris {
			if err := h.DidChange(uri, version, concurrentDoc(i, version)); err != nil {
				t.Fatalf("DidChange doc%d v%d: %v", i, version, err)
			}
		}
		// Read while those analyses are still running. Errors are fine (the
		// document is mid-flight); a hang or a crash is not.
		for _, uri := range uris {
			_, _ = h.Hover(uri, 20, 8)
			_, _ = h.Completion(uri, 20, 8)
		}
	}
	// Let the in-flight analyses land so the test does not return while
	// goroutines are still touching its temp files.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for i, uri := range uris {
		if _, err := h.WaitForDiagnostics(ctx, uri); err != nil {
			t.Fatalf("doc%d never published diagnostics: %v", i, err)
		}
	}

	// The server must still be answering after all that.
	for i, uri := range uris {
		if _, err := h.DocumentSymbol(uri); err != nil {
			t.Fatalf("doc%d unresponsive after concurrent analyses: %v", i, err)
		}
	}

	// Drain before returning. WaitForDiagnostics is not sufficient on its own:
	// DidChange cancels the previous analysis for a URI, but cancellation is
	// only checked after analyzeFile has already returned, so a superseded
	// analysis still runs to completion and publishes nothing. Waiting on
	// publications therefore lets up to docs*rounds analyses — each fanning out
	// its own pool of parse workers — run on into whatever test follows. That is
	// not hypothetical: without this drain, the suite's timing-sensitive
	// diagnostics tests began failing while passing in isolation.
	//
	// The handler exposes no completion signal, so the goroutine count is the
	// honest proxy. Slack and a deadline keep it from being brittle.
	deadline := time.Now().Add(60 * time.Second)
	for runtime.NumGoroutine() > baseline+2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > baseline+2 {
		t.Logf("drain incomplete: %d goroutines still live (baseline %d)", n, baseline)
	}
}
