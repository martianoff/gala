// Package go_generic_inference_bridge provides generic Go functions whose
// type parameters GALA infers through a generic defined function type
// (`Pred[T]`) or past an untyped constant argument (`init E`).
package go_generic_inference_bridge

// Pred is a generic defined function type.
type Pred[T any] func(T) bool

// CountP counts the elements of xs that p accepts.
func CountP[T any](xs []T, p Pred[T]) int {
	n := 0
	for _, x := range xs {
		if p(x) {
			n++
		}
	}
	return n
}

// Fold folds s from init with f.
func Fold[S ~[]E, E any](s S, init E, f func(E, E) E) E {
	acc := init
	for _, x := range s {
		acc = f(acc, x)
	}
	return acc
}
