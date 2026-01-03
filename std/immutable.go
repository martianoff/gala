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

func (i Immutable[T]) Copy() Immutable[T] {
	return NewImmutable(Copy(i.value))
}

func (i Immutable[T]) Equal(other Immutable[T]) bool {
	return Equal(i.value, other.value)
}

var _ Unapply = (*Immutable[any])(nil)
var _ Copyable[Immutable[any]] = (*Immutable[any])(nil)
var _ Equatable[Immutable[any]] = (*Immutable[any])(nil)
