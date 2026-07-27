// Package concurrency holds the compile-time data-race-safety machinery for
// GALA. Its first and only member (PR1) is the Shareable type predicate: a
// pure, side-effect-free decision procedure that reports whether a value of a
// given GALA type may safely cross a goroutine boundary — i.e. be captured by
// or passed to a Future / Spawn.
//
// The predicate answers the question "is this type deeply immutable (and free
// of shared mutable aliasing)?". Only deeply-immutable values can be shared
// across goroutines without a data race, because concurrent reads of an
// immutable value never conflict.
//
// This package is intentionally decoupled from the transformer (which is
// per-file, stateful, and heavy). It depends only on the transpiler package for
// the Type definitions and TypeMetadata, and nothing in the transpiler depends
// on it — so there is no import cycle and the analyzer (the eventual enforcement
// consumer in a later PR) can import it freely.
//
// NOTE: This PR wires the predicate into nothing. There is no enforcement, no
// `Sendable` bound, no diagnostic, and no surface change. It is the reviewable
// foundation the later capture-analysis (PR2) and enforcement (PR3) build on.
package concurrency

import (
	"martianoff/gala/internal/transpiler"
)

// MetadataResolver looks up the structural metadata for a named GALA type — its
// ordered fields with per-field val/var flags, or its sealed variants. The
// analyzer supplies this from richAST.Types; a test supplies a stub.
//
// It must return (nil, false) for any type it does not recognise as a
// user-defined GALA struct or sealed type. In particular it returns false for
// opaque Go-interop types, bare type parameters, and unresolved names. The
// checker treats an unresolved named type conservatively as NOT shareable, so a
// resolver that under-reports only ever errs on the safe side.
type MetadataResolver func(named transpiler.Type) (*transpiler.TypeMetadata, bool)

// Checker decides shareability. It carries an optional MetadataResolver used to
// resolve the fields of user-defined struct and sealed types (that structural
// information lives in TypeMetadata, not in the Type value itself). Value-kinded
// types — primitives, collections, and the std wrappers — are decided purely
// from the Type and need no resolver.
type Checker struct {
	resolve      MetadataResolver
	goUnderlying GoUnderlyingResolver
}

// GoUnderlyingResolver reports the underlying type of a Go named type (e.g.
// time.Duration -> int64), or (nil, false) when the type is not a resolvable Go
// named type. It is how the checker recognises Go scalar value types — named
// types whose underlying is a primitive — as shareable. It is deliberately NOT
// used for Go structs: Go has no immutability and auto-takes a pointer receiver
// for `d.Method()`, so an all-value-field Go struct still cannot be proven
// non-aliased/non-mutated; only scalar (field-less) underlyings are accepted.
type GoUnderlyingResolver func(named transpiler.Type) (transpiler.Type, bool)

// NewChecker builds a Checker. A nil resolver is allowed: the checker then
// treats every user-defined struct/sealed type as unresolvable, hence not
// shareable. Use a real resolver (backed by richAST.Types) whenever struct and
// sealed shareability must be decided.
func NewChecker(resolve MetadataResolver) *Checker {
	if resolve == nil {
		resolve = func(transpiler.Type) (*transpiler.TypeMetadata, bool) { return nil, false }
	}
	return &Checker{resolve: resolve}
}

// SetGoUnderlyingResolver wires an optional Go-named-type underlying resolver so
// the checker can recognise Go scalar value types (time.Duration, os.FileMode,
// …) as shareable. Without it, such types stay conservatively not-shareable.
func (c *Checker) SetGoUnderlyingResolver(fn GoUnderlyingResolver) {
	c.goUnderlying = fn
}

// IsShareable reports whether a value of type t may safely cross a goroutine
// boundary. It is deep and transitive, and terminates on recursive types via a
// visited set.
//
// This method form (rather than a bare free function) exists because deciding a
// struct or sealed type requires its field metadata, which is not carried by the
// Type value. See the package-level IsShareable for a resolver-free convenience.
func (c *Checker) IsShareable(t transpiler.Type) bool {
	return c.isShareable(t, map[string]bool{})
}

// IsShareable is a resolver-free convenience matching the conceptual
// `IsShareable(t Type) bool` signature. It decides every value-kinded type
// (primitives, immutable/mutable collections, Option/Try/Either/tuple, the
// Immutable/ConstPtr wrappers, functions, pointers, slices, maps) correctly, but
// because it has no MetadataResolver it cannot see the fields of a user-defined
// struct or sealed type and so returns false for those. Callers that need
// struct/sealed decisions must use NewChecker(resolver).IsShareable.
func IsShareable(t transpiler.Type) bool {
	return NewChecker(nil).IsShareable(t)
}

func (c *Checker) isShareable(t transpiler.Type, visited map[string]bool) bool {
	// nil interface or the analyzer's "gave up" NilType placeholder: be
	// conservative and never crash on a nil input.
	if transpiler.IsUnusable(t) {
		return false
	}

	switch v := t.(type) {
	case transpiler.VoidType:
		// Unit / void: no value, nothing to race on.
		return true

	case transpiler.BasicType:
		return c.isBasicShareable(v, visited)

	case transpiler.NamedType:
		// A named type with no type arguments. It is either a std wrapper used
		// without params (unusual), a user-defined struct/sealed type, or an
		// opaque Go-interop type. Wrapper/primitive names are decided by name;
		// everything else falls to metadata resolution.
		if b, decided := c.decideByName(v.Name, nil, visited); decided {
			return b
		}
		if c.isNamedStructShareable(t, nil, visited) {
			return true
		}
		// A Go named type whose underlying is a primitive scalar (e.g.
		// time.Duration -> int64, os.FileMode -> uint32) is a self-contained
		// value: no fields to alias, no reachable heap, no pointer receiver that
		// could mutate shared state. Such a value is safe to capture across a
		// goroutine boundary. (Go STRUCTS are intentionally excluded — see
		// GoUnderlyingResolver.)
		return c.isGoScalarShareable(t)

	case transpiler.GenericType:
		return c.isGenericShareable(v, visited)

	case transpiler.ArrayType:
		// Bare Go slice ([]T): backed by a shared, mutable backing array.
		return false

	case transpiler.MapType:
		// Bare Go map: mutable and not safe for concurrent access.
		return false

	case transpiler.PointerType:
		// Bare Go pointer: shared mutable aliasing. (The safe read-only pointer
		// wrapper is std.ConstPtr, handled as a GenericType.)
		return false

	case transpiler.FuncType:
		// A closure's shareability depends on what it captures, which is not
		// decidable from its type alone. Capture analysis (a later PR) decides
		// whether a closure passed to a Sendable parameter is safe; this type
		// predicate conservatively reports false.
		return false
	}

	// Unknown/unhandled kind: conservative.
	return false
}

// isGoScalarShareable reports whether a Go named type is a scalar value type —
// its underlying resolves to a primitive (time.Duration -> int64). Only a
// primitive-scalar underlying is accepted; a struct, array, pointer, slice or
// map underlying is rejected (Go structs cannot be proven non-aliased and Go
// auto-takes a pointer receiver for methods, so an all-value-field struct is
// still unsound to treat as shareable). Returns false when no Go-underlying
// resolver is wired (the resolver-free convenience path stays conservative).
func (c *Checker) isGoScalarShareable(named transpiler.Type) bool {
	if c.goUnderlying == nil {
		return false
	}
	u, ok := c.goUnderlying(named)
	if !ok || transpiler.IsUnusable(u) {
		return false
	}
	bt, isBasic := u.(transpiler.BasicType)
	return isBasic && isShareablePrimitive(bt.Name)
}

// isBasicShareable decides a BasicType. Primitive value types are shareable.
// Anything else is either a bare (unbounded) type parameter — conservatively not
// shareable until a Shareable bound proves otherwise in a later PR — or a
// user-defined type from a main/test package that resolveBaseName lowered to a
// bare name; the latter is resolved via metadata.
func (c *Checker) isBasicShareable(v transpiler.BasicType, visited map[string]bool) bool {
	if isShareablePrimitive(v.Name) {
		return true
	}
	// Might be a struct/sealed type declared in a main/test package (those
	// resolve to a bare BasicType rather than a package-qualified NamedType).
	if _, ok := c.resolve(v); ok {
		return c.isNamedStructShareable(v, nil, visited)
	}
	// Otherwise: a bare type parameter or an unresolved name. Conservative.
	return false
}

// isGenericShareable decides a parametrised type: std wrappers, immutable /
// mutable collections, and generic user structs/sealed types.
func (c *Checker) isGenericShareable(v transpiler.GenericType, visited map[string]bool) bool {
	base := v.Base
	name := base.BaseName()
	pkg := base.GetPackage()

	// std wrappers and mutable/immutable collections are decided by name+package.
	if b, decided := c.decideCollection(name, pkg, v.Params, visited); decided {
		return b
	}
	if b, decided := c.decideByName(name, v.Params, visited); decided {
		return b
	}

	// A generic user-defined struct or sealed type, e.g. Box[int]. Resolve its
	// metadata and check each field with the type parameters substituted.
	return c.isNamedStructShareable(base, v.Params, visited)
}

// decideCollection handles the collection packages. The immutable and mutable
// collection packages expose types with THE SAME names (Array, List, HashMap,
// HashSet, TreeMap, TreeSet), so the package qualifier is the only reliable
// discriminator. Anything from collection_immutable is shareable iff all of its
// type arguments are; anything from collection_mutable is never shareable.
//
// Returns (result, true) when the type belongs to a collection package, else
// (false, false) to let the caller try other rules.
func (c *Checker) decideCollection(name, pkg string, params []transpiler.Type, visited map[string]bool) (bool, bool) {
	switch pkg {
	case pkgCollectionImmutable:
		return c.allShareable(params, visited), true
	case pkgCollectionMutable:
		// Mutable collections are backed by shared mutable state.
		return false, true
	}
	return false, false
}

// decideByName handles the std wrapper types, which are recognised by base name
// regardless of package qualifier (they are reserved std names; dot-imported std
// can drop the qualifier). Returns (result, true) when name is a known wrapper.
func (c *Checker) decideByName(name string, params []transpiler.Type, visited map[string]bool) (bool, bool) {
	switch stripPkg(name) {
	case transpiler.TypeOption, transpiler.TypeTry, transpiler.TypeEither,
		transpiler.TypeTuple, transpiler.TypeTuple3, transpiler.TypeTuple4,
		transpiler.TypeTuple5, transpiler.TypeTuple6, transpiler.TypeTuple7,
		transpiler.TypeTuple8, transpiler.TypeTuple9, transpiler.TypeTuple10:
		// Option[T] / Try[T] / Either[A,B] / TupleN[...]: shareable iff every
		// type argument is shareable.
		return c.allShareable(params, visited), true

	case transpiler.TypeImmutable, transpiler.TypeConstPtr:
		// Immutable[T] and ConstPtr[T].
		//
		// DEVIATION FROM SPEC (documented in docs/CONCURRENCY_SAFETY.MD):
		// the spec lists these as unconditionally shareable. They are only
		// SOUNDLY shareable when their payload T is itself shareable.
		//
		//   type Immutable[T] struct { var value T }
		//   func (i Immutable[T]) Get() T = i.value
		//
		// Immutable freezes reassignment of `value`, but Get() hands back the
		// stored T by copy. If T is a reference-backed mutable type (e.g. a
		// collection_mutable.Array, whose header aliases a Go slice), the copy
		// still aliases the shared backing store — two goroutines could then
		// mutate/read it concurrently. ConstPtr[T] holds a *T and only forbids
		// writes THROUGH the ConstPtr; it cannot stop another holder of the
		// underlying value from mutating it. So both are shareable exactly when
		// T is shareable. For the common case (Immutable[int], ConstPtr[string])
		// this is still true, matching intent, but it refuses the unsound
		// Immutable[MutableArray[int]].
		return c.allShareable(params, visited), true
	}
	return false, false
}

// isNamedStructShareable resolves a struct or sealed type's metadata and decides
// it structurally. typeArgs are the instantiation's type arguments (nil for a
// non-generic type); they are substituted for the declaration's type parameters
// before each field is checked.
//
// visited prevents infinite recursion on self-referential types (e.g. a cons
// list). Re-entering an in-progress type returns true co-inductively: a cycle on
// its own introduces no mutability, so shareability is governed by the other
// fields, which are still checked.
func (c *Checker) isNamedStructShareable(named transpiler.Type, typeArgs []transpiler.Type, visited map[string]bool) bool {
	meta, ok := c.resolve(named)
	if !ok || meta == nil {
		// Unresolved: opaque Go-interop type, unknown name, or (with a nil
		// resolver) any user type. Conservative.
		return false
	}

	// The visited key must include the instantiation's type arguments, not just
	// the type name: a generic type can recursively reference itself at a
	// DIFFERENT fixed argument (e.g. `struct Node[T](val v T, val other
	// Node[MutableArray[int]])`). Keying on the name alone would short-circuit
	// the inner `Node[MutableArray[int]]` to "shareable" before its differing
	// (mutable) argument is checked — an unsound false negative. Including the
	// args makes Node[int] and Node[MutableArray[int]] distinct keys, so the
	// cycle only terminates on a genuine same-argument self-reference.
	key := metaKey(meta) + instantiationKey(typeArgs)
	if visited[key] {
		return true
	}
	visited[key] = true
	defer delete(visited, key)

	subst := buildSubst(meta.TypeParams, typeArgs)

	if meta.IsSealed {
		// Sealed type: shareable iff every field of every variant is shareable.
		// Sealed-variant fields are constructor parameters and are always
		// immutable (there is no `var` variant field), so only their types are
		// checked.
		for i := range meta.SealedVariants {
			for _, ft := range meta.SealedVariants[i].FieldTypes {
				if !c.isShareable(substitute(ft, subst), visited) {
					return false
				}
			}
		}
		return true
	}

	// Plain struct: shareable iff every field is an immutable (`val`) field
	// whose type is shareable. Any `var` field makes the whole struct unsafe.
	for i, fieldName := range meta.FieldNames {
		if i < len(meta.ImmutFlags) && !meta.ImmutFlags[i] {
			return false // a `var` (mutable) field
		}
		ft, ok := meta.Fields[fieldName]
		if !ok {
			return false // metadata inconsistency: be conservative
		}
		if !c.isShareable(substitute(ft, subst), visited) {
			return false
		}
	}
	return true
}

// allShareable reports whether every type argument is shareable. An empty
// argument list is vacuously shareable (e.g. a would-be raw wrapper), but in
// practice the wrappers this is called for always carry arguments.
func (c *Checker) allShareable(params []transpiler.Type, visited map[string]bool) bool {
	for _, p := range params {
		if !c.isShareable(p, visited) {
			return false
		}
	}
	return true
}
