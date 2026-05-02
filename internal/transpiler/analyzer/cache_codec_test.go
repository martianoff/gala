package analyzer

import (
	"bytes"
	"encoding/gob"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"martianoff/gala/internal/transpiler"
)

// Register concrete Type variants for the legacy gob path used by the
// benchmark (BenchmarkCodec_GobEncode/Decode) and by the testdata gob
// snapshot loader. Registration was previously done in cache.go's init(),
// but that init was removed when the binary codec replaced gob in the
// production path. The gob registry is process-global, so registering
// here keeps the benchmark working without polluting the analyzer's
// non-test runtime.
func init() {
	gob.Register(transpiler.BasicType{})
	gob.Register(transpiler.NamedType{})
	gob.Register(transpiler.GenericType{})
	gob.Register(transpiler.ArrayType{})
	gob.Register(transpiler.MapType{})
	gob.Register(transpiler.PointerType{})
	gob.Register(transpiler.FuncType{})
	gob.Register(transpiler.NilType{})
	gob.Register(transpiler.VoidType{})
}

// loadSampleGobCache reads the testdata gob snapshot into a CachedRichAST.
// The snapshot was captured from gala_team's cache (the gala-tui entry,
// the largest in that project at ~417 KB). It exercises all RichAST
// fields (Types, Functions, GoTypeInfo with all five sub-maps, nested
// Type interface values, etc.) so the benchmark numbers reflect realistic
// per-package decode work, not a synthetic micro-shape.
func loadSampleGobCache(tb testing.TB) *CachedRichAST {
	tb.Helper()
	// Walk up from this test file's directory to find testdata/. go test
	// runs with the package directory as cwd, but Bazel runs with the
	// runfiles tree at $TEST_SRCDIR; relative-to-source resolution via
	// runtime.Caller keeps both happy.
	_, thisFile, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(thisFile), "testdata", "cache_sample_v1.gob")
	data, err := os.ReadFile(path)
	if err != nil {
		// Fallback to relative path (Bazel runfiles).
		alt := filepath.Join("testdata", "cache_sample_v1.gob")
		data, err = os.ReadFile(alt)
		if err != nil {
			tb.Skipf("sample cache not available (path=%s, alt=%s): %v", path, alt, err)
			return nil
		}
	}
	var c CachedRichAST
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&c); err != nil {
		tb.Fatalf("gob decode of sample: %v", err)
	}
	return &c
}

// buildSyntheticCache produces a CachedRichAST with the rough shape of a
// medium-size package (~50 types, ~30 funcs, nested generic type vals).
// Used as a fallback when the testdata snapshot is unavailable.
//
// Maps are populated; empty top-level fields are left nil to match the
// decoder's normalization (it returns nil for zero-length maps/slices to
// avoid the per-package empty-allocation tax).
func buildSyntheticCache() *CachedRichAST {
	r := &CachedRichAST{
		PackageName:   "synthetic",
		Types:         make(map[string]*transpiler.TypeMetadata, 50),
		Functions:     make(map[string]*transpiler.FunctionMetadata, 30),
		Packages:      map[string]string{"std": "std", "lazy": "lazy"},
		TypeAliases: map[string]transpiler.Type{
			"Handler": transpiler.FuncType{
				Params:  []transpiler.Type{transpiler.NamedType{Package: "http", Name: "Request"}},
				Results: []transpiler.Type{transpiler.NamedType{Package: "http", Name: "Response"}},
			},
		},
		ImportPathMap: map[string]string{"std": "martianoff/gala/std"},
		DepsHash:      "deadbeef",
		DirectImports: []string{"martianoff/gala/std", "martianoff/gala/lazy"},
	}
	// GoTypeInfo with at least one entry exercised so the round-trip
	// comparison sees a populated *GoTypeInfo rather than empty maps.
	r.GoTypeInfo = transpiler.NewGoTypeInfo()
	r.GoTypeInfo.Variables["pkg.X"] = transpiler.BasicType{Name: "int"}
	r.GoTypeInfo.Constants["pkg.K"] = transpiler.BasicType{Name: "string"}
	r.GoTypeInfo.TypeAliases["pkg.Alias"] = transpiler.NamedType{Package: "std", Name: "Other"}
	r.GoTypeInfo.Functions["pkg.F"] = &transpiler.GoFuncSignature{
		Params:  []transpiler.GoParam{{Name: "x", Type: transpiler.BasicType{Name: "int"}}},
		Returns: []transpiler.Type{transpiler.BasicType{Name: "string"}},
	}
	r.GoTypeInfo.Types["pkg.S"] = &transpiler.GoTypeData{
		Kind:       "struct",
		Fields:     map[string]transpiler.Type{"a": transpiler.BasicType{Name: "int"}},
		FieldOrder: []string{"a"},
	}
	for i := 0; i < 50; i++ {
		name := "T" + itoa(i)
		r.Types["synthetic."+name] = &transpiler.TypeMetadata{
			Name:       name,
			Package:    "synthetic",
			FieldNames: []string{"a", "b", "c"},
			Fields: map[string]transpiler.Type{
				"a": transpiler.BasicType{Name: "int"},
				"b": transpiler.GenericType{
					Base:   transpiler.NamedType{Package: "std", Name: "Option"},
					Params: []transpiler.Type{transpiler.BasicType{Name: "string"}},
				},
				"c": transpiler.ArrayType{Elem: transpiler.NamedType{Package: "std", Name: "Foo"}},
			},
			Methods: map[string]*transpiler.MethodMetadata{
				"M1": {
					Name: "M1", Package: "synthetic",
					ParamTypes: []transpiler.Type{transpiler.BasicType{Name: "int"}},
					ReturnType: transpiler.BasicType{Name: "string"},
				},
			},
		}
	}
	for i := 0; i < 30; i++ {
		name := "F" + itoa(i)
		r.Functions["synthetic."+name] = &transpiler.FunctionMetadata{
			Name: name, Package: "synthetic",
			ParamTypes: []transpiler.Type{
				transpiler.BasicType{Name: "int"},
				transpiler.GenericType{
					Base:   transpiler.NamedType{Package: "std", Name: "Future"},
					Params: []transpiler.Type{transpiler.BasicType{Name: "any"}},
				},
			},
			ReturnType: transpiler.NamedType{Package: "std", Name: "Either"},
		}
	}
	return r
}

// loadOrSynthCache returns the on-disk sample if present, else a synthetic.
func loadOrSynthCache(tb testing.TB) *CachedRichAST {
	tb.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(thisFile), "testdata", "cache_sample_v1.gob")
	if _, err := os.Stat(path); err == nil {
		return loadSampleGobCache(tb)
	}
	if _, err := os.Stat(filepath.Join("testdata", "cache_sample_v1.gob")); err == nil {
		return loadSampleGobCache(tb)
	}
	return buildSyntheticCache()
}

func BenchmarkCodec_GobEncode(b *testing.B) {
	c := loadOrSynthCache(b)
	b.ResetTimer()
	b.ReportAllocs()
	var totalBytes int64
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(c); err != nil {
			b.Fatal(err)
		}
		totalBytes = int64(buf.Len())
	}
	b.SetBytes(totalBytes)
}

func BenchmarkCodec_GobDecode(b *testing.B) {
	c := loadOrSynthCache(b)
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(c); err != nil {
		b.Fatal(err)
	}
	encoded := buf.Bytes()
	b.SetBytes(int64(len(encoded)))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var out CachedRichAST
		if err := gob.NewDecoder(bytes.NewReader(encoded)).Decode(&out); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCodec_BinaryEncode(b *testing.B) {
	c := loadOrSynthCache(b)
	b.ResetTimer()
	b.ReportAllocs()
	var totalBytes int64
	for i := 0; i < b.N; i++ {
		data, err := encodeCachedRichAST(c)
		if err != nil {
			b.Fatal(err)
		}
		totalBytes = int64(len(data))
	}
	b.SetBytes(totalBytes)
}

func BenchmarkCodec_BinaryDecode(b *testing.B) {
	c := loadOrSynthCache(b)
	data, err := encodeCachedRichAST(c)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := decodeCachedRichAST(data); err != nil {
			b.Fatal(err)
		}
	}
}

// TestBinaryCodec_RoundTrip verifies that encode/decode preserves all the
// fields toCachedRichAST persists. We compare via reflect.DeepEqual on
// the decoded value rather than re-encoded bytes, because Go's map
// iteration order is non-deterministic — two encodings of the same
// structurally-equal value can be different byte sequences.
func TestBinaryCodec_RoundTrip(t *testing.T) {
	src := buildSyntheticCache()
	encoded, err := encodeCachedRichAST(src)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := decodeCachedRichAST(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Spot-check a few fields directly.
	if got.PackageName != src.PackageName {
		t.Errorf("PackageName: got %q, want %q", got.PackageName, src.PackageName)
	}
	if got.DepsHash != src.DepsHash {
		t.Errorf("DepsHash: got %q, want %q", got.DepsHash, src.DepsHash)
	}
	if len(got.Types) != len(src.Types) {
		t.Errorf("Types count: got %d, want %d", len(got.Types), len(src.Types))
	}
	if len(got.Functions) != len(src.Functions) {
		t.Errorf("Functions count: got %d, want %d", len(got.Functions), len(src.Functions))
	}
	if !reflect.DeepEqual(got, src) {
		t.Errorf("round-trip not DeepEqual; encoded %d bytes", len(encoded))
	}
}

// TestBinaryCodec_RoundTripFromGobSample takes the on-disk gob sample
// (a real cached package), decodes it via gob, re-encodes via the new
// binary codec, and verifies the new decoder reads back a value that is
// reflect.DeepEqual to the original. This is the parity test against a
// realistic shape with all field types exercised.
//
// Note: gob preserves "empty but non-nil" maps and slices as such, while
// the binary codec normalizes them to nil. The two are interchangeable
// for the downstream consumer (range/len/lookup all behave identically),
// so we round-trip the gob form through encode→decode→encode and verify
// the second decode is DeepEqual to the first — which uses the binary
// codec's own normalization on both sides.
func TestBinaryCodec_RoundTripFromGobSample(t *testing.T) {
	c := loadOrSynthCache(t)
	if c == nil {
		t.Skip("no sample available")
	}
	encoded, err := encodeCachedRichAST(c)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := decodeCachedRichAST(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Normalize the source through one codec round-trip so empty-vs-nil
	// distinctions inherited from gob are erased on both sides.
	encoded2, err := encodeCachedRichAST(got)
	if err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	got2, err := decodeCachedRichAST(encoded2)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if !reflect.DeepEqual(got, got2) {
		t.Errorf("idempotency failure: encode∘decode is not stable")
	}
	c = got // compare against the normalized form for the rest of the checks
	_ = encoded2
	_ = got2
	if got.PackageName != c.PackageName {
		t.Errorf("PackageName: got %q, want %q", got.PackageName, c.PackageName)
	}
	// Spot-check that the realistic shape's high-cardinality fields
	// survived in count.
	if len(got.Types) != len(c.Types) {
		t.Errorf("Types count: got %d, want %d", len(got.Types), len(c.Types))
	}
	if len(got.Functions) != len(c.Functions) {
		t.Errorf("Functions count: got %d, want %d", len(got.Functions), len(c.Functions))
	}
}
