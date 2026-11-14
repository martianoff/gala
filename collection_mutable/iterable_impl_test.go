package collection_mutable

import "martianoff/gala/std"

// Compile-time checks: the methods are on the pointer and return pointers, so
// the pointer (GALA's Ptr[Array[T]]) implements std.Iterable with itself as the
// self type. A signature drift on either side fails the build.
var (
	_ std.Iterable[int, *Array[int]] = (*Array[int])(nil)
	_ std.Iterable[int, *List[int]]  = (*List[int])(nil)
)
