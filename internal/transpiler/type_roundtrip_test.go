package transpiler

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// stripImportPaths returns typ with every NamedType's ImportPath cleared.
func stripImportPaths(typ Type) Type {
	switch v := typ.(type) {
	case NamedType:
		v.ImportPath = ""
		return v
	case GenericType:
		params := make([]Type, len(v.Params))
		for i, p := range v.Params {
			params[i] = stripImportPaths(p)
		}
		return GenericType{Base: stripImportPaths(v.Base), Params: params}
	case ArrayType:
		return ArrayType{Elem: stripImportPaths(v.Elem)}
	case PointerType:
		return PointerType{Elem: stripImportPaths(v.Elem)}
	case MapType:
		return MapType{Key: stripImportPaths(v.Key), Elem: stripImportPaths(v.Elem)}
	case FuncType:
		out := FuncType{}
		for _, p := range v.Params {
			out.Params = append(out.Params, stripImportPaths(p))
		}
		for _, r := range v.Results {
			out.Results = append(out.Results, stripImportPaths(r))
		}
		return out
	}
	return typ
}

// TestParseTypeRoundTrip pins what a String -> ParseType round trip keeps, for
// the type shapes the transformer produces: everything except a NamedType's
// ImportPath.
//
// ImportPath cannot survive, because String() prints a type the way GALA
// source spells it and the source spelling has no import path. The transformer
// therefore must not rely on it after a round trip. Two things make that
// hold: emitting a pathless Go type resolves its qualifier through this file's
// Go import of that name (resolveTypeQualifier), and a Go type's methods and
// fields are looked up under its package's real name taken from the import
// path while the path is still attached (goTypeLookupName).
func TestParseTypeRoundTrip(t *testing.T) {
	builder := NamedType{Package: "strings", Name: "Builder", ImportPath: "strings"}
	aliased := NamedType{Package: "gostrings", Name: "Builder", ImportPath: "strings"}
	fileInfo := NamedType{Package: "fs", Name: "FileInfo", ImportPath: "io/fs"}
	option := NamedType{Package: "std", Name: "Option"}

	cases := []struct {
		name string
		typ  Type
	}{
		{"basic", BasicType{Name: "int64"}},
		{"local named", BasicType{Name: "Millis"}},
		{"go named", builder},
		{"go named under an alias", aliased},
		{"go named whose package shares a GALA name", fileInfo},
		{"pointer", PointerType{Elem: builder}},
		{"slice", ArrayType{Elem: aliased}},
		{"map", MapType{Key: BasicType{Name: "string"}, Elem: PointerType{Elem: builder}}},
		{"generic", GenericType{Base: option, Params: []Type{PointerType{Elem: aliased}}}},
		{"nested generic", GenericType{
			Base:   NamedType{Package: "std", Name: "Tuple"},
			Params: []Type{fileInfo, GenericType{Base: option, Params: []Type{BasicType{Name: "int"}}}},
		}},
		{"func", FuncType{Params: []Type{PointerType{Elem: builder}}, Results: []Type{BasicType{Name: "string"}}}},
		{"func without params or results", FuncType{}},
		{"func with several results", FuncType{
			Params:  []Type{BasicType{Name: "int"}, fileInfo},
			Results: []Type{BasicType{Name: "string"}, BasicType{Name: "error"}},
		}},
		{"generic over func types", GenericType{Base: option, Params: []Type{
			FuncType{Params: []Type{BasicType{Name: "int"}, BasicType{Name: "int"}}, Results: []Type{BasicType{Name: "int"}}},
		}}},
		{"func returning func", FuncType{Results: []Type{FuncType{Params: []Type{aliased}}}}},
		{"map keyed by a generic", MapType{
			Key:  GenericType{Base: option, Params: []Type{BasicType{Name: "int"}}},
			Elem: MapType{Key: BasicType{Name: "string"}, Elem: builder},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseType(tc.typ.String())
			assert.Equal(t, tc.typ.String(), got.String(), "printed form must be stable")
			assert.Equal(t, stripImportPaths(tc.typ), stripImportPaths(got), "only ImportPath may be lost")
		})
	}
}
