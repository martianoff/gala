package collection_immutable

import "martianoff/gala/std"

// Compile-time checks: Array and List implement std.Iterable with themselves as
// the self type, so a signature drift on either side fails the build.
var (
	_ std.Iterable[int, Array[int]] = Array[int]{}
	_ std.Iterable[int, List[int]]  = List[int]{}
)
