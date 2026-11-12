package collection_immutable

import "martianoff/gala/std"

// Compile-time checks: the sequence collections implement std.Iterable with
// themselves as the self type, so a signature drift on either side fails the
// build. HashSet, TreeSet and the maps are not Iterable: they have no
// positional methods (Tail, Take, SplitAt, ...).
var (
	_ std.Iterable[int, Array[int]] = Array[int]{}
	_ std.Iterable[int, List[int]]  = List[int]{}
)
