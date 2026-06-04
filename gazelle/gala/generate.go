package gala

import (
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

// mainFuncRe matches a zero-argument main function declaration, used together
// with a "package main" declaration to identify a gala_binary. This is a
// lightweight content check, not a re-parse of the grammar.
var mainFuncRe = regexp.MustCompile(`(?m)^\s*func\s+main\s*\(\s*\)`)

// galaImports is the opaque payload carried from GenerateRules to Resolve for
// each generated rule (GenerateResult.Imports).
type galaImports struct {
	// imports is the deduped set of GALA import paths across the rule's files.
	imports []string
	// self is the rule's own importpath, excluded from its own deps.
	self string
}

// GenerateRules implements language.Language. It produces one gala_library (or
// gala_binary for a "package main" with a main()) for the non-test sources in a
// directory, plus a gala_test for any *_test.gala files.
func (gl *galaLang) GenerateRules(args language.GenerateArgs) language.GenerateResult {
	gc := getGalaConfig(args.Config)

	var galaFiles []string
	for _, f := range args.RegularFiles {
		if strings.HasSuffix(f, ".gala") {
			galaFiles = append(galaFiles, f)
		}
	}
	if len(galaFiles) == 0 {
		return language.GenerateResult{}
	}

	infos, err := extractImports(gl.runner, gc.Helper, args.Dir, galaFiles)
	if err != nil {
		log.Printf("gazelle(gala): %s: extracting imports: %v", args.Rel, err)
		infos = map[string]fileInfo{}
	}

	// Partition into library/binary sources and framework-test files. A
	// *_test.gala file that declares `package main` with a `main()` is a
	// runnable benchmark binary, not a framework test — those are hand-wired
	// gala_binary targets (e.g. perf_gala), so the extension leaves them alone
	// rather than minting a colliding gala_test.
	var srcFiles, testFiles []string
	for _, f := range galaFiles {
		if strings.HasSuffix(f, "_test.gala") {
			if fileIsMain(args.Dir, f, infos) {
				log.Printf("gazelle(gala): %s: skipping %s (package main with main(); manage it as a gala_binary by hand)", args.Rel, f)
				continue
			}
			testFiles = append(testFiles, f)
		} else {
			srcFiles = append(srcFiles, f)
		}
	}

	var res language.GenerateResult
	name := dirName(args.Rel)

	// Non-test sources: gala_library, or gala_binary for a runnable main.
	if len(srcFiles) > 0 {
		sort.Strings(srcFiles)
		isMain := detectMain(args.Dir, srcFiles, infos)
		importPath := joinImportPath(gc.Prefix, args.Rel)

		var r *rule.Rule
		if isMain {
			r = rule.NewRule("gala_binary", name)
			r.SetAttr("srcs", srcFiles)
		} else {
			r = rule.NewRule("gala_library", name)
			r.SetAttr("srcs", srcFiles)
			r.SetAttr("importpath", importPath)
			r.SetAttr("visibility", []string{"//visibility:public"})
		}
		res.Gen = append(res.Gen, r)
		res.Imports = append(res.Imports, &galaImports{
			imports: collectImports(srcFiles, infos),
			self:    importPath,
		})
	}

	// Test sources: one gala_test per *_test.gala file, named after the file
	// stem, carrying that file's own resolved deps. This matches the repo
	// convention (one test target per file) and avoids name collisions with
	// existing per-file rules when regenerating.
	sort.Strings(testFiles)
	for _, tf := range testFiles {
		testName := strings.TrimSuffix(tf, ".gala")
		r := rule.NewRule("gala_test", testName)
		r.SetAttr("srcs", []string{tf})
		res.Gen = append(res.Gen, r)
		res.Imports = append(res.Imports, &galaImports{
			imports: collectImports([]string{tf}, infos),
			self:    "",
		})
	}

	return res
}

// dirName returns the rule base name for a directory: the final path segment,
// or "root" for the repository root.
func dirName(rel string) string {
	if rel == "" {
		return "root"
	}
	return path.Base(rel)
}

// joinImportPath builds the importpath for a directory from the prefix and the
// repo-relative directory path.
func joinImportPath(prefix, rel string) string {
	if rel == "" {
		return prefix
	}
	return prefix + "/" + rel
}

// detectMain reports whether the source set forms a runnable binary: at least
// one file declares "package main" and contains a zero-arg main() function.
func detectMain(dir string, srcFiles []string, infos map[string]fileInfo) bool {
	for _, f := range srcFiles {
		if fileIsMain(dir, f, infos) {
			return true
		}
	}
	return false
}

// fileIsMain reports whether a single file declares "package main" and defines
// a zero-arg main() function.
func fileIsMain(dir, f string, infos map[string]fileInfo) bool {
	if info, ok := infos[f]; !ok || info.Package != "main" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dir, f))
	if err != nil {
		return false
	}
	return mainFuncRe.Match(data)
}

// collectImports returns the sorted, deduped union of import paths declared by
// the given files.
func collectImports(files []string, infos map[string]fileInfo) []string {
	set := map[string]bool{}
	for _, f := range files {
		info, ok := infos[f]
		if !ok {
			continue
		}
		for _, imp := range info.Imports {
			set[imp] = true
		}
	}
	out := make([]string, 0, len(set))
	for imp := range set {
		out = append(out, imp)
	}
	sort.Strings(out)
	return out
}
