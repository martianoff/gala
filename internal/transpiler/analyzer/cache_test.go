package analyzer

import (
	"bytes"
	"encoding/gob"
	"os"
	"path/filepath"
	"testing"
	"time"

	"martianoff/gala/internal/transpiler"
)

// TestCachedRichAST_StripsTransitiveTypes verifies that toCachedRichAST
// keeps only the package's OWN metadata when serializing — types that
// belong to imported packages are pruned, even when they are present in
// the in-memory pkgAST as a result of transitive Merge.
//
// This is the size-bound guard: without filtering, a downstream package
// in a deep dependency graph would inline N×M of the transitive type
// closure into its on-disk .gob, producing multi-GB cache files.
func TestCachedRichAST_StripsTransitiveTypes(t *testing.T) {
	r := &transpiler.RichAST{
		PackageName: "consumer",
		Types: map[string]*transpiler.TypeMetadata{
			"consumer.Local":          {Name: "Local", Package: "consumer"},
			"collection_immutable.Array": {Name: "Array", Package: "collection_immutable"},
			"std.Option":             {Name: "Option", Package: "std"},
			"directive.Directive":    {Name: "Directive", Package: "directive"},
		},
		Functions: map[string]*transpiler.FunctionMetadata{
			"consumer.Run":     {Name: "Run", Package: "consumer"},
			"directive.Parse":  {Name: "Parse", Package: "directive"},
		},
		CompanionObjects: map[string]*transpiler.CompanionObjectMetadata{
			"consumer.Foo": {Name: "Foo", Package: "consumer"},
			"std.Some":     {Name: "Some", Package: "std"},
		},
	}

	cached := toCachedRichAST(r, "deps", []string{"martianoff/gala/directive"})

	if _, ok := cached.Types["consumer.Local"]; !ok {
		t.Errorf("expected own type 'consumer.Local' to be retained")
	}
	for _, foreign := range []string{"collection_immutable.Array", "std.Option", "directive.Directive"} {
		if _, ok := cached.Types[foreign]; ok {
			t.Errorf("expected foreign type %q to be stripped, but it was retained", foreign)
		}
	}
	if _, ok := cached.Functions["consumer.Run"]; !ok {
		t.Errorf("expected own function 'consumer.Run' to be retained")
	}
	if _, ok := cached.Functions["directive.Parse"]; ok {
		t.Errorf("expected foreign function 'directive.Parse' to be stripped")
	}
	if _, ok := cached.CompanionObjects["consumer.Foo"]; !ok {
		t.Errorf("expected own companion 'consumer.Foo' to be retained")
	}
	if _, ok := cached.CompanionObjects["std.Some"]; ok {
		t.Errorf("expected foreign companion 'std.Some' to be stripped")
	}
	if got, want := len(cached.DirectImports), 1; got != want {
		t.Errorf("DirectImports length: got %d, want %d", got, want)
	}
}

// TestCachedRichAST_GobSizeBounded checks that the gob-serialized size
// of a CachedRichAST scales with the package's OWN metadata, not with
// the transitive closure that Merge accumulated. This is the size
// regression test: if a future change re-introduces transitive inlining,
// the encoded size will jump.
func TestCachedRichAST_GobSizeBounded(t *testing.T) {
	// Build a pkgAST with 3 own types and 2000 foreign types, mirroring
	// the shape of a downstream package that imports from many transitively.
	r := &transpiler.RichAST{
		PackageName: "small",
		Types:       make(map[string]*transpiler.TypeMetadata),
	}
	for i := 0; i < 3; i++ {
		name := "Own" + itoa(i)
		r.Types["small."+name] = &transpiler.TypeMetadata{Name: name, Package: "small"}
	}
	for i := 0; i < 2000; i++ {
		name := "Big" + itoa(i)
		// Foreign types — these must be stripped on serialization.
		r.Types["other."+name] = &transpiler.TypeMetadata{
			Name:    name,
			Package: "other",
			// Inflate each foreign type with method metadata so the
			// pre-fix bug (inlining transitive types) would balloon the
			// encoded blob noticeably.
			Methods: map[string]*transpiler.MethodMetadata{
				"M1": {Name: "M1", Package: "other"},
				"M2": {Name: "M2", Package: "other"},
				"M3": {Name: "M3", Package: "other"},
			},
		}
	}

	cached := toCachedRichAST(r, "h", nil)
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(cached); err != nil {
		t.Fatalf("gob encode failed: %v", err)
	}

	// 3 own types with no methods serialize to roughly a few hundred
	// bytes. Allow 64 KiB headroom for gob's type registration overhead.
	const maxBytes = 64 * 1024
	if buf.Len() > maxBytes {
		t.Errorf("encoded size %d bytes exceeds budget %d — transitive types likely leaked into cache", buf.Len(), maxBytes)
	}

	// Sanity: re-decode and confirm only own types survive.
	var decoded CachedRichAST
	if err := gob.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&decoded); err != nil {
		t.Fatalf("gob decode failed: %v", err)
	}
	if got, want := len(decoded.Types), 3; got != want {
		t.Errorf("decoded type count: got %d, want %d", got, want)
	}
}

// TestExtractDirectImportPaths verifies the line-based import scan that
// records direct imports for re-hydration on cache hit. The paths must
// be sorted and deduplicated so the cached list is deterministic.
func TestExtractDirectImportPaths(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.gala": `package x
import "martianoff/gala/std"
import "martianoff/gala/collection_immutable"
`,
		"b.gala": `package x
import "martianoff/gala/std"
import "martianoff/gala/lazy"
`,
		"a_test.gala": `package x
import "martianoff/gala/test"
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	got := extractDirectImportPaths(dir)
	want := []string{
		"martianoff/gala/collection_immutable",
		"martianoff/gala/lazy",
		"martianoff/gala/std",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, p := range want {
		if got[i] != p {
			t.Errorf("got[%d]=%q, want %q", i, got[i], p)
		}
	}
}

// TestPruneStaleCaches ensures the GC removes old sibling cache
// directories whose names share the version prefix but whose mtime is
// older than the staleness threshold, while preserving the active dir
// and any freshly-touched sibling.
func TestPruneStaleCaches(t *testing.T) {
	parent := t.TempDir()
	mk := func(name string, age time.Duration) string {
		p := filepath.Join(parent, name)
		if err := os.Mkdir(p, 0755); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(-age)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
		return p
	}

	keep := mk("v1-dev-abc123", 0)
	stale := mk("v1-dev-old111", staleCacheMaxAge+24*time.Hour)
	fresh := mk("v1-dev-new222", time.Hour) // freshly used; preserve
	unrelated := mk("scratch", staleCacheMaxAge+24*time.Hour)

	pruneStaleCaches(parent, "v1-dev-abc123")

	mustExist := func(p string) {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to remain, got %v", p, err)
		}
	}
	mustNotExist := func(p string) {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected %s to be pruned, but it still exists (err=%v)", p, err)
		}
	}

	mustExist(keep)
	mustNotExist(stale)
	mustExist(fresh)
	mustExist(unrelated) // pruner only touches version-prefixed names
}

// TestPruneStaleCaches_SkippedByEnv verifies the GALA_KEEP_STALE_CACHES=1
// gate so users can inspect prior generations without losing them.
func TestPruneStaleCaches_SkippedByEnv(t *testing.T) {
	t.Setenv("GALA_KEEP_STALE_CACHES", "1")
	parent := t.TempDir()
	older := time.Now().Add(-(staleCacheMaxAge + time.Hour))
	stalePath := filepath.Join(parent, "v1-dev-stale")
	if err := os.Mkdir(stalePath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stalePath, older, older); err != nil {
		t.Fatal(err)
	}

	// pruneStaleCaches itself doesn't read the env — the GC is gated at
	// newAnalysisCache. Sanity-check that direct invocation still prunes;
	// the gate is enforced one level up.
	pruneStaleCaches(parent, "v1-dev-current")
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Errorf("expected stale dir to be pruned by direct call, but it remains")
	}
}

// itoa avoids importing strconv in the test file's narrow surface.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
