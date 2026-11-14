package ownership

import (
	"martianoff/gala/internal/transpiler"
)

// immutableCollections are the persistent collections of
// collection_immutable. Their nodes are shared between versions, but no
// version is ever mutated, so to a translation each is a value: it is
// owned-shaped when its elements are.
var immutableCollections = map[string]bool{
	"Array": true, "List": true, "HashMap": true, "HashSet": true, "TreeMap": true, "TreeSet": true,
}

// valueBasics are the basic types whose values hold no reference.
var valueBasics = map[string]bool{
	"bool": true, "string": true, "byte": true, "rune": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "uintptr": true,
	"float32": true, "float64": true, "complex64": true, "complex128": true,
}

// shape is "" when t is owned-shaped (docs/OWNERSHIP_DESIGN.MD 3.1, "Shape
// restriction"): a value that holds no reference another binding could
// share, so a pointer to it can be translated as the value itself. Otherwise
// it says why not.
func (a *pkg) shape(t transpiler.Type) string {
	if t == nil {
		return a.shapeOf(t, nil)
	}
	// Only a whole answer is memoized: one found inside a cycle rests on the
	// cycle's assumption that the rest of it is owned-shaped.
	if why, ok := a.shapes[t.String()]; ok {
		return why
	}
	why := a.shapeOf(t, map[string]bool{})
	a.shapes[t.String()] = why
	return why
}

func (a *pkg) shapeOf(t transpiler.Type, visiting map[string]bool) string {
	switch tt := t.(type) {
	case nil:
		return "a type that did not resolve"
	case transpiler.BasicType:
		if valueBasics[tt.Name] {
			return ""
		}
		if a.typeKey(tt) != "" {
			// A type of the package recorded by its bare name.
			return a.shapeNamed(transpiler.NamedType{Name: tt.Name}, visiting)
		}
		return "holds " + tt.Name
	case transpiler.PointerType:
		return "holds a pointer " + tt.String()
	case transpiler.ArrayType:
		if tt.Fixed {
			return a.shapeOf(tt.Elem, visiting)
		}
		return "holds a Go slice " + tt.String()
	case transpiler.MapType:
		return "holds a Go map " + tt.String()
	case transpiler.ChanType:
		return "holds a channel"
	case transpiler.FuncType:
		return "holds a function " + tt.String()
	case transpiler.InterfaceType:
		return "holds an interface"
	case transpiler.StructType:
		for _, f := range tt.Fields {
			if why := a.shapeOf(f.Type, visiting); why != "" {
				return why
			}
		}
		return ""
	case transpiler.NamedType, transpiler.GenericType:
		return a.shapeNamed(t, visiting)
	}
	return "holds " + t.String()
}

// shapeNamed is shape for a named type, generic or not.
func (a *pkg) shapeNamed(t transpiler.Type, visiting map[string]bool) string {
	var args []transpiler.Type
	if g, ok := t.(transpiler.GenericType); ok {
		args = g.Params
	}
	key := a.typeKey(t)
	if n, ok := baseNamed(t); ok {
		if valueBasics[n.Name] && n.Package == "" {
			return ""
		}
		if n.Package == "collection_immutable" && immutableCollections[n.Name] {
			for _, arg := range args {
				if why := a.shapeOf(arg, visiting); why != "" {
					return why
				}
			}
			return ""
		}
	}
	memoKey := t.String()
	if visiting[memoKey] {
		// A recursive value type, through a sealed case or a collection:
		// owned-shaped if the rest of it is.
		return ""
	}
	visiting[memoKey] = true
	defer delete(visiting, memoKey)
	return a.shapeMeta(t, key, visiting)
}

func (a *pkg) shapeMeta(t transpiler.Type, key string, visiting map[string]bool) string {
	tm, sub := a.typeMeta(t)
	if tm == nil {
		return "holds " + t.String() + ", a type without GALA metadata"
	}
	switch {
	case tm.IsInterface:
		return "holds an interface " + t.String()
	case tm.IsOpaque:
		return a.shapeOf(tm.Underlying, visiting)
	case len(tm.TypeParams) > 0 && sub == nil:
		return "holds " + t.String() + " with unknown type arguments"
	}
	if tm.IsSealed {
		for _, v := range tm.SealedVariants {
			for i, ft := range v.FieldTypes {
				if why := a.shapeOf(unwrapImmutable(substitute(ft, sub)), visiting); why != "" {
					return why + " (case " + v.Name + ", field " + v.FieldNames[i] + ")"
				}
			}
		}
		return ""
	}
	if len(tm.FieldNames) == 0 && len(tm.Fields) == 0 && tm.Methods != nil && a.rich.GoTypeInfo != nil {
		if _, isGo := a.rich.GoTypeInfo.Types[key]; isGo {
			return "holds the Go type " + t.String()
		}
	}
	for _, name := range tm.FieldNames {
		ft := tm.Fields[name]
		if why := a.shapeOf(unwrapImmutable(substitute(ft, sub)), visiting); why != "" {
			return "field " + name + " " + why
		}
	}
	return ""
}

// baseNamed is t's named type, the base of a generic one.
func baseNamed(t transpiler.Type) (transpiler.NamedType, bool) {
	switch tt := t.(type) {
	case transpiler.NamedType:
		return tt, true
	case transpiler.GenericType:
		return baseNamed(tt.Base)
	}
	return transpiler.NamedType{}, false
}
