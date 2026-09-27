package analyzer

import "go/types"

// noCopyReason names the type that makes a value of typ unsafe to copy, or
// returns "" when a copy is fine. It follows go vet's copylocks check: typ is
// unsafe when it, or any field reached through struct fields and array
// elements (not through pointers, slices or maps), is
//
//   - a struct type of package sync or sync/atomic (Mutex, RWMutex, WaitGroup,
//     Once, Cond, Map, Pool, atomic.Int64, atomic.Value, ...);
//   - a struct whose pointer has Lock() and Unlock() methods its value lacks
//     (vet's sync.Locker test);
//   - a struct type named noCopy, the conventional marker;
//   - strings.Builder, which panics when used after being copied, or
//     bytes.Buffer, whose writes to a copy are lost.
//
// The transpiler reports a pointer-receiver call on such a value when Go
// cannot address it (GALA-E0053), since the call would run on a copy.
func noCopyReason(typ types.Type) string {
	return noCopyPath(typ, make(map[types.Type]bool))
}

func noCopyPath(typ types.Type, seen map[types.Type]bool) string {
	for {
		arr, ok := typ.Underlying().(*types.Array)
		if !ok {
			break
		}
		typ = arr.Elem()
	}
	if seen[typ] {
		return ""
	}
	seen[typ] = true
	st, isStruct := typ.Underlying().(*types.Struct)
	if !isStruct {
		return ""
	}
	if named, ok := types.Unalias(typ).(*types.Named); ok && named.Obj().Pkg() != nil {
		obj := named.Obj()
		switch path := obj.Pkg().Path(); {
		case path == "sync" || path == "sync/atomic",
			path == "strings" && obj.Name() == "Builder",
			path == "bytes" && obj.Name() == "Buffer",
			obj.Name() == "noCopy",
			isPointerOnlyLocker(typ):
			return obj.Pkg().Name() + "." + obj.Name()
		}
	}
	for i := 0; i < st.NumFields(); i++ {
		if reason := noCopyPath(st.Field(i).Type(), seen); reason != "" {
			return reason
		}
	}
	return ""
}

// isPointerOnlyLocker reports whether *typ has Lock() and Unlock() methods
// and typ itself does not: a value that locks through its own address.
func isPointerOnlyLocker(typ types.Type) bool {
	// This runs for every struct reached from every extracted Go type, so the
	// cheap single-name lookups go first; nearly every type stops here.
	ptr := types.NewPointer(typ)
	for _, name := range []string{"Lock", "Unlock"} {
		obj, _, _ := types.LookupFieldOrMethod(ptr, false, nil, name)
		fn, ok := obj.(*types.Func)
		if !ok {
			return false
		}
		if sig := fn.Type().(*types.Signature); sig.Params().Len() != 0 || sig.Results().Len() != 0 {
			return false
		}
	}
	valSet := types.NewMethodSet(typ)
	return valSet.Lookup(nil, "Lock") == nil || valSet.Lookup(nil, "Unlock") == nil
}
