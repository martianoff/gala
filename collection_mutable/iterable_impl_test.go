package collection_mutable

import "martianoff/gala/std"

// Compile-time checks: the mutable sequence collections declare every method on
// a pointer receiver and return pointers, so the pointer (GALA's Ptr[Array[T]])
// implements std.Iterable with itself as the self type. A signature drift on
// either side fails the build. The sets and maps are not Iterable: they have
// no positional methods (Tail, Take, SplitAt, ...).
var (
	_ std.Iterable[int, *Array[int]] = (*Array[int])(nil)
	_ std.Iterable[int, *List[int]]  = (*List[int])(nil)
)
