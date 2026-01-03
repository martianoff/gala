package std

import "reflect"

type Immutable[T any] struct {
	value T
}

func NewImmutable[T any](v T) Immutable[T] {
	return Immutable[T]{value: v}
}

func (i Immutable[T]) Get() T {
	return i.value
}

func (i Immutable[T]) Unapply(v any) bool {
	return reflect.DeepEqual(i.value, v)
}

var _ Unapply = (*Immutable[any])(nil)
