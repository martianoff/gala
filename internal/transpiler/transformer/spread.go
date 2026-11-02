package transformer

import (
	"go/token"

	"martianoff/gala/internal/transpiler"
)

// ellipsisPos returns a non-zero token.Pos if hasSpread is true, indicating variadic expansion.
func ellipsisPos(hasSpread bool) token.Pos {
	if hasSpread {
		return token.Pos(1)
	}
	return token.NoPos
}

// spreadArgType is the type a spread argument `xs...` is lowered against when
// it fills a variadic parameter recorded as elem: a slice of elem, so a slice
// of nullable pointers filling a `...Ptr[T]` is rejected as an `Array[*T]` is
// where an `Array[Ptr[T]]` is (GALA-E0080). An unknown elem stays unknown.
func spreadArgType(elem transpiler.Type) transpiler.Type {
	if transpiler.IsUnusable(elem) {
		return elem
	}
	return transpiler.ArrayType{Elem: elem}
}
