// Package testcorpus is a frozen copy of GALA sources for golden tests: the
// standard library, the other libraries and the examples, as they were when it
// was taken (see testdata/corpus.txtar). A golden over it changes only when
// the code under test, or the corpus itself, does: an edit of std or of an
// example leaves it alone, and so does the machine's checkout layout or line
// ending.
package testcorpus

import (
	_ "embed"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
)

//go:embed testdata/corpus.txtar
var archive string

// Corpus is the corpus extracted to a directory.
type Corpus struct {
	// Root is the directory the corpus is extracted to: its import root, as a
	// search path of the analyzer.
	Root string
}

// Files is the corpus's files, by slash-separated path relative to its root,
// to their texts. They end their lines with LF.
var Files = sync.OnceValue(func() map[string]string {
	files := map[string]string{}
	// A checkout may have turned the archive's line ends into CRLF.
	text := strings.ReplaceAll(archive, "\r\n", "\n")
	name := ""
	var body strings.Builder
	flush := func() {
		if name != "" {
			files[name] = body.String()
		}
		body.Reset()
	}
	for _, line := range strings.SplitAfter(text, "\n") {
		if strings.HasPrefix(line, "-- ") && strings.HasSuffix(line, " --\n") {
			flush()
			name = strings.TrimSuffix(strings.TrimPrefix(line, "-- "), " --\n")
			continue
		}
		body.WriteString(line)
	}
	flush()
	return files
})

// Extract is the corpus extracted to a temporary directory. Under Bazel it is
// extracted once per test binary, in the test's TEST_TMPDIR (which Bazel
// removes), and every test of the binary reads the same copy, so none may
// write in it; elsewhere, into a directory of tb's own.
func Extract(tb testing.TB) Corpus {
	tb.Helper()
	var c Corpus
	var err error
	if os.Getenv("TEST_TMPDIR") != "" {
		c, err = extracted()
	} else {
		c, err = extractTo(tb.TempDir())
	}
	if err != nil {
		tb.Fatalf("cannot extract the corpus: %v", err)
	}
	return c
}

var extracted = sync.OnceValues(func() (Corpus, error) {
	root, err := os.MkdirTemp(os.Getenv("TEST_TMPDIR"), "testcorpus")
	if err != nil {
		return Corpus{}, err
	}
	return extractTo(root)
})

// extractTo writes the corpus into root.
func extractTo(root string) (Corpus, error) {
	made := map[string]bool{}
	for name, text := range Files() {
		path := filepath.Join(root, filepath.FromSlash(name))
		if dir := filepath.Dir(path); !made[dir] {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return Corpus{}, err
			}
			made[dir] = true
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return Corpus{}, err
		}
	}
	return Corpus{Root: root}, nil
}

// Path is the path of the corpus file rel (slash-separated, relative to the
// root).
func (c Corpus) Path(rel string) string { return filepath.Join(c.Root, filepath.FromSlash(rel)) }

// Rel is path relative to the root, slash-separated.
func (c Corpus) Rel(path string) string {
	rel, err := filepath.Rel(c.Root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// GalaNames is the names of the corpus's .gala files (see Files), sorted.
func GalaNames() []string {
	var names []string
	for name := range Files() {
		if strings.HasSuffix(name, ".gala") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// GalaFiles is the paths of the corpus's .gala files, in the order of their
// slash-separated names (GalaNames), which is the same on every platform.
func (c Corpus) GalaFiles() []string {
	names := GalaNames()
	paths := make([]string, len(names))
	for i, name := range names {
		paths[i] = c.Path(name)
	}
	return paths
}

// Normalizer rewrites the machine-specific directories in what a golden
// records (the corpus root, a test's temporary directories) to names, so that
// the golden is the same on every machine and platform.
type Normalizer struct {
	r    *strings.Replacer
	rest *regexp.Regexp
}

// NewNormalizer rewrites each directory of dirs to its name, as written
// either with forward or with back slashes, raw or escaped as in JSON, and
// with either case of a drive letter. The rest of a path under one is
// rewritten with forward slashes.
func NewNormalizer(dirs map[string]string) *Normalizer {
	// The longest directory first, so that one under another is rewritten
	// as itself.
	sorted := make([]string, 0, len(dirs))
	for dir := range dirs {
		sorted = append(sorted, dir)
	}
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	var pairs, names []string
	for _, dir := range sorted {
		name := dirs[dir]
		names = append(names, regexp.QuoteMeta(name))
		back := filepath.FromSlash(dir)
		for _, form := range []string{filepath.ToSlash(dir), back, strings.ReplaceAll(back, `\`, `\\`)} {
			pairs = append(pairs, form, name)
			if len(form) > 1 && form[1] == ':' {
				other := strings.ToLower(form[:1])
				if other == form[:1] {
					other = strings.ToUpper(form[:1])
				}
				pairs = append(pairs, other+form[1:], name)
			}
		}
	}
	return &Normalizer{
		r:    strings.NewReplacer(pairs...),
		rest: regexp.MustCompile(`(?:` + strings.Join(names, "|") + `)[^\s"'(),;:` + "`" + `]*`),
	}
}

// Replace is s with its directories rewritten.
func (n *Normalizer) Replace(s string) string {
	s = n.r.Replace(s)
	if !strings.Contains(s, `\`) {
		return s
	}
	return n.rest.ReplaceAllStringFunc(s, func(p string) string {
		return strings.ReplaceAll(strings.ReplaceAll(p, `\\`, "/"), `\`, "/")
	})
}

// Normalizer rewrites the corpus root to $ROOT.
func (c Corpus) Normalizer() *Normalizer { return NewNormalizer(map[string]string{c.Root: "$ROOT"}) }
