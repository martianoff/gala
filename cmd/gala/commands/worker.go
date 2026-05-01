package commands

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"martianoff/gala/cmd/gala/commands/worker"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// runWorker is invoked when gala is launched with --persistent_worker by
// Bazel. The process stays alive for the lifetime of the build invocation
// (one worker process per worker_key), reading WorkRequests off stdin and
// writing WorkResponses to stdout. Because the analyzer's expensive
// warm-up — loading the std/collection_immutable type graph, resolving
// transitive .gala dependencies — depends ONLY on the search-path set,
// we cache one BatchAnalyzer per (search paths, goroot) tuple so all
// per-package transpiles in a build amortize warm-up to a single cold
// start. The previous genrule path forked a fresh gala binary per
// package and paid the cold start ~100 times for a project the size of
// gala_team (PR #316: 794 s on ubuntu-latest for cmd/main.gala alone,
// dominated by analyzer cold starts in transitive transpile actions).
//
// Reset semantics — each request gets:
//   - A fresh package-files set on the cached BatchAnalyzer
//     (SetPackageFiles already resets checkedDirs internally so directory
//     scanning works correctly per file)
//   - A fresh transformer + generator pair (they hold per-file state)
//   - Per-request stdout/stderr captured into a bytes.Buffer and shipped
//     back via WorkResponse.Output (Bazel reserves real stdout for proto
//     framing; a stray fmt.Println would corrupt the byte stream and
//     crash the worker).
//
// What is NOT reset across requests (intentionally):
//   - BatchAnalyzer.inner.analyzedPkgs / analyzedPkgImports — the type
//     graph IS the warm cache. PR #316's "own-types-only projection" fix
//     guarantees this is safe to reuse across requests with the same
//     search paths.
//   - parser, generator templates — pure / stateless across requests.
//
// Plain (non-worker) `gala transpile <args>` mode is unchanged; this is a
// strictly additive code path.
func runWorker() {
	stdin := bufio.NewReaderSize(os.Stdin, 1<<16)
	stdout := bufio.NewWriterSize(os.Stdout, 1<<16)

	for {
		req, err := worker.ReadRequest(stdin)
		if err == io.EOF {
			return // Bazel closed stdin; clean shutdown.
		}
		if err != nil {
			// Protocol-level corruption: the worker can no longer
			// reliably frame responses, so exit non-zero and let
			// Bazel restart us. Any in-flight response is forfeit.
			fmt.Fprintf(os.Stderr, "gala worker: read request: %v\n", err)
			os.Exit(2)
		}
		if req.Cancel {
			// Cancellation isn't supported — gala transpile is
			// short enough per package that we don't bother
			// interrupting it. Reply with was_cancelled=true so
			// Bazel doesn't deadlock waiting for a real response.
			_ = worker.WriteResponse(stdout, &worker.WorkResponse{
				RequestID:    req.RequestID,
				WasCancelled: true,
			})
			_ = stdout.Flush()
			continue
		}

		resp := handleRequest(req)
		if err := worker.WriteResponse(stdout, resp); err != nil {
			fmt.Fprintf(os.Stderr, "gala worker: write response: %v\n", err)
			os.Exit(2)
		}
		if err := stdout.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "gala worker: flush: %v\n", err)
			os.Exit(2)
		}
	}
}

// handleRequest dispatches a single WorkRequest. It must NEVER write to
// real stdout (proto framing only); diagnostics go through the response
// Output field. Recovers from panics so one buggy request can't kill the
// whole worker — Bazel would just rerun the action against a fresh
// process anyway, but a contained panic preserves cache state for the
// remaining requests in the build.
func handleRequest(req *worker.WorkRequest) *worker.WorkResponse {
	var out bytes.Buffer
	exitCode := 0
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(&out, "gala worker: panic handling request: %v\n", r)
			exitCode = 1
		}
	}()

	exitCode = dispatchTranspile(req.Arguments, &out)
	return &worker.WorkResponse{
		ExitCode:  int32(exitCode),
		Output:    out.String(),
		RequestID: req.RequestID,
	}
}

// dispatchTranspile runs the transpile action described by argv inside the
// worker process. It mirrors the cobra command structure but uses a fresh
// flag.FlagSet per request so global cobra state (transpileInput etc.)
// is not mutated — that would break concurrent worker requests if Bazel
// ever ran them on a single worker without a mutex (current Bazel
// runs one request per worker process at a time, but multiplex workers
// with `supports-multiplex-workers` may multiplex; we stay correct
// regardless).
//
// The first argument selects the sub-command: "transpile" or
// "transpile-package", matching the cobra subcommands. Anything else is
// rejected with a clear diagnostic.
func dispatchTranspile(argv []string, out io.Writer) int {
	if len(argv) == 0 {
		fmt.Fprintln(out, "gala worker: empty arguments")
		return 1
	}
	switch argv[0] {
	case "transpile-package":
		return runWorkerTranspilePackage(argv[1:], out)
	case "transpile":
		return runWorkerTranspile(argv[1:], out)
	default:
		// Some callers pass --input/--output as the first arg (the
		// implicit "gala transpile" mode); accept those too.
		if strings.HasPrefix(argv[0], "-") {
			return runWorkerTranspile(argv, out)
		}
		fmt.Fprintf(out, "gala worker: unknown sub-command %q\n", argv[0])
		return 1
	}
}

// analyzerCache memoizes BatchAnalyzers keyed by (search paths, goroot,
// project root). All requests in a build that share these inputs reuse
// the same analyzer and its warmed-up package cache, which is the entire
// point of persistent-worker mode.
//
// We do NOT key by package-files set: SetPackageFiles is called per
// request and is cheap (it only updates the file list and clears
// checkedDirs).
//
// Bound: in practice all per-package transpiles within ONE Bazel build
// of one consumer share the same search-path set (project root +
// transitive deps) so we expect 1-2 entries per worker session.
type analyzerCacheEntry struct {
	parser   transpiler.GalaParser
	analyzer *analyzer.BatchAnalyzer
}

var (
	analyzerCacheMu sync.Mutex
	analyzerCache   = map[string]*analyzerCacheEntry{}
)

func cacheKey(searchPaths []string, goroot, projectRoot string) string {
	h := sha256.New()
	for _, p := range searchPaths {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	h.Write([]byte("|goroot="))
	h.Write([]byte(goroot))
	h.Write([]byte("|root="))
	h.Write([]byte(projectRoot))
	return hex.EncodeToString(h.Sum(nil))
}

// getBatchAnalyzer returns a cached BatchAnalyzer for the given search
// paths, creating one if necessary. The first request in a build pays
// the cold-start cost (loading std + transitive type graph); subsequent
// requests share that warm state.
//
// Cache key: (sorted search paths, GOROOT, project root). All requests
// in a single Bazel build of one consumer share the same key, so we
// expect 1-2 entries per worker session.
func getBatchAnalyzer(searchPaths []string, goroot, projectRoot string) (transpiler.GalaParser, *analyzer.BatchAnalyzer) {
	// Sort search paths deterministically so callers passing the same
	// set in different orders share a single cache slot.
	sorted := append([]string(nil), searchPaths...)
	sort.Strings(sorted)
	key := cacheKey(sorted, goroot, projectRoot)

	analyzerCacheMu.Lock()
	defer analyzerCacheMu.Unlock()
	if entry, ok := analyzerCache[key]; ok {
		return entry.parser, entry.analyzer
	}
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewBatchAnalyzer(p, searchPaths, projectRoot)
	analyzerCache[key] = &analyzerCacheEntry{parser: p, analyzer: a}
	return p, a
}

// runWorkerTranspilePackage handles `transpile-package` requests inside
// the worker. Mirrors runTranspilePackage in transpile_package.go but:
//   - parses flags from argv into local vars (no globals)
//   - reuses a cached BatchAnalyzer across requests with the same
//     search-path set (the warm-cache win)
//   - writes per-request diagnostics to `out` instead of os.Stderr
func runWorkerTranspilePackage(argv []string, out io.Writer) int {
	fs := flag.NewFlagSet("transpile-package", flag.ContinueOnError)
	fs.SetOutput(out)
	var (
		inputs  string
		outputs string
		search  string
		goroot  string
	)
	fs.StringVar(&inputs, "inputs", "", "")
	fs.StringVar(&outputs, "outputs", "", "")
	fs.StringVar(&search, "search", ".", "")
	fs.StringVar(&goroot, "goroot", "", "")
	if err := fs.Parse(argv); err != nil {
		fmt.Fprintf(out, "transpile-package: parse flags: %v\n", err)
		return 1
	}
	if inputs == "" || outputs == "" {
		fmt.Fprintln(out, "transpile-package: --inputs and --outputs are required")
		return 1
	}

	inList := strings.Split(inputs, ",")
	outList := strings.Split(outputs, ",")
	if len(inList) != len(outList) {
		fmt.Fprintf(out, "transpile-package: inputs (%d) != outputs (%d)\n", len(inList), len(outList))
		return 1
	}

	if goroot != "" {
		// GOROOT is read by go/importer at use time. Setting it once
		// per worker is fine — Bazel actions in a single build share
		// one Go SDK so the value is invariant. We still reset it on
		// every request for paranoia (cost: one syscall).
		os.Setenv("GOROOT", goroot)
	}

	paths := strings.Split(search, ",")
	paths = autoResolveSearchPaths(inList[0], paths)
	projectRoot := findGalaModDir(filepath.Dir(mustAbs(inList[0])))

	parser, batch := getBatchAnalyzer(paths, goroot, projectRoot)

	hasError := false
	for i, inputPath := range inList {
		outputPath := outList[i]
		content, err := os.ReadFile(inputPath)
		if err != nil {
			fmt.Fprintf(out, "Error reading %s: %v\n", inputPath, err)
			hasError = true
			continue
		}

		// Set siblings for THIS file. SetPackageFiles also resets
		// checkedDirs so per-file directory scanning starts fresh.
		var packageFiles []string
		for j, other := range inList {
			if j != i {
				packageFiles = append(packageFiles, other)
			}
		}
		batch.SetPackageFiles(packageFiles)

		tr := transformer.NewGalaASTTransformer()
		g := generator.NewGoCodeGenerator()
		t := transpiler.NewGalaToGoTranspiler(parser, batch, tr, g)

		goCode, err := t.Transpile(string(content), inputPath)
		if err != nil {
			fmt.Fprintf(out, "Error transpiling %s: %v\n", inputPath, err)
			hasError = true
			continue
		}

		if outDir := filepath.Dir(outputPath); outDir != "" && outDir != "." {
			os.MkdirAll(outDir, 0755)
		}
		if err := os.WriteFile(outputPath, []byte(goCode), 0644); err != nil {
			fmt.Fprintf(out, "Error writing %s: %v\n", outputPath, err)
			hasError = true
			continue
		}
	}

	if hasError {
		return 1
	}
	return 0
}

// runWorkerTranspile handles single-file `transpile` requests inside the
// worker. Mirrors runTranspile in transpile.go but reuses a cached
// analyzer keyed by search-path set. Note: we deliberately fall back to
// NewGalaAnalyzerWithPackageFiles for the single-file case because the
// single-file caller may pass --package-files for sibling-aware type
// resolution; the BatchAnalyzer's SetPackageFiles + per-call construction
// of transformer/generator is the equivalent.
func runWorkerTranspile(argv []string, out io.Writer) int {
	fs := flag.NewFlagSet("transpile", flag.ContinueOnError)
	fs.SetOutput(out)
	var (
		input        string
		output       string
		search       string
		packageFiles string
		goroot       string
	)
	fs.StringVar(&input, "input", "", "")
	fs.StringVar(&output, "output", "", "")
	fs.StringVar(&search, "search", ".", "")
	fs.StringVar(&packageFiles, "package-files", "", "")
	fs.StringVar(&goroot, "goroot", "", "")
	if err := fs.Parse(argv); err != nil {
		fmt.Fprintf(out, "transpile: parse flags: %v\n", err)
		return 1
	}
	if input == "" {
		// Allow positional argument for the implicit-transpile shape
		// some callers use (`gala foo.gala -o foo.go`).
		if rest := fs.Args(); len(rest) > 0 {
			input = rest[0]
		}
	}
	if input == "" {
		fmt.Fprintln(out, "transpile: --input is required")
		return 1
	}

	if goroot != "" {
		os.Setenv("GOROOT", goroot)
	}

	content, err := os.ReadFile(input)
	if err != nil {
		fmt.Fprintf(out, "Error reading %s: %v\n", input, err)
		return 1
	}

	paths := strings.Split(search, ",")
	paths = autoResolveSearchPaths(input, paths)
	projectRoot := findGalaModDir(filepath.Dir(mustAbs(input)))

	parser, batch := getBatchAnalyzer(paths, goroot, projectRoot)

	var pkgFiles []string
	if packageFiles != "" {
		pkgFiles = strings.Split(packageFiles, ",")
	}
	batch.SetPackageFiles(pkgFiles)

	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	t := transpiler.NewGalaToGoTranspiler(parser, batch, tr, g)

	goCode, err := t.Transpile(string(content), input)
	if err != nil {
		fmt.Fprintf(out, "Error transpiling %s: %v\n", input, err)
		return 1
	}

	if output == "" {
		fmt.Fprint(out, goCode)
		return 0
	}
	if outDir := filepath.Dir(output); outDir != "" && outDir != "." {
		os.MkdirAll(outDir, 0755)
	}
	if err := os.WriteFile(output, []byte(goCode), 0644); err != nil {
		fmt.Fprintf(out, "Error writing %s: %v\n", output, err)
		return 1
	}
	return 0
}

// mustAbs returns the absolute path of p, falling back to p itself if the
// resolution fails (matches behavior of inputs that may already be
// absolute or live in the genrule sandbox).
func mustAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// workerCmd hides the worker behind an explicit subcommand so unit tests
// can exercise it deterministically. Bazel itself launches the worker
// via the top-level --persistent_worker flag (see root.go), which is the
// shape rules_go and rules_jvm_external use.
var workerCmd = &cobra.Command{
	Use:    "persistent-worker",
	Short:  "Run as a Bazel persistent worker (advanced)",
	Hidden: true,
	Run: func(cmd *cobra.Command, args []string) {
		runWorker()
	},
}
