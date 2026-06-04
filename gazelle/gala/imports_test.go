package gala

import (
	"reflect"
	"testing"
)

func TestParseImports(t *testing.T) {
	data := []byte(`[
		{"file":"a.gala","package":"main","imports":[{"path":"fmt","alias":"","dot":false},{"path":"martianoff/gala/collection_immutable","alias":"","dot":true}],"error":""},
		{"file":"broken.gala","package":"","imports":[],"error":"parse error at line 3"}
	]`)
	got, err := parseImports(data)
	if err != nil {
		t.Fatalf("parseImports: %v", err)
	}
	if _, ok := got["broken.gala"]; ok {
		t.Errorf("file with error should be skipped, but was present")
	}
	a, ok := got["a.gala"]
	if !ok {
		t.Fatalf("a.gala missing from result")
	}
	if a.Package != "main" {
		t.Errorf("package = %q, want main", a.Package)
	}
	want := []string{"fmt", "martianoff/gala/collection_immutable"}
	if !reflect.DeepEqual(a.Imports, want) {
		t.Errorf("imports = %v, want %v", a.Imports, want)
	}
}

func TestParseImportsInvalidJSON(t *testing.T) {
	if _, err := parseImports([]byte("not json")); err == nil {
		t.Errorf("expected error for invalid JSON, got nil")
	}
}
