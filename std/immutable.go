package std

type Immutable[T any] struct {
	value T
}

func NewImmutable[T any](v T) Immutable[T] {
	return Immutable[T]{value: v}
}

func (i Immutable[T]) Get() T {
	return i.value
}
