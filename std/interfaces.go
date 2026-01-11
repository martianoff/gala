package std

type Unapply[U any, T any] interface {
	Unapply(u U) Option[T]
}

type Copyable[T any] interface {
	Copy() T
}

type Equatable[T any] interface {
	Equal(other T) bool
}
