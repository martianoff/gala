package std

type Unapply interface {
	Unapply(v any) bool
}

type Copyable[T any] interface {
	Copy() T
}

type Equatable[T any] interface {
	Equal(other T) bool
}
