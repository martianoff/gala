package std

// Compile-time interface compliance checks for Unapply interface
// These verify that option.gala and either.gala's Unapply methods implement the Unapply interface

// Some[T] implements Unapply[Option[T], T] - extracts value from Option
var _ Unapply[Option[any], any] = Some[any]{}

// None[T] is a sealed type 0-field variant - its Unapply returns bool directly (not Option[bool])

// Left[A, B] implements Unapply[Either[A, B], A] - extracts left value from Either
var _ Unapply[Either[any, any], any] = Left[any, any]{}

// Right[A, B] implements Unapply[Either[A, B], B] - extracts right value from Either
var _ Unapply[Either[any, any], any] = Right[any, any]{}
