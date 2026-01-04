package std

import "reflect"

type Option[T any] struct {
	Value   T
	Defined bool
}

func Some[T any](v T) Option[T] {
	return Option[T]{Value: v, Defined: true}
}

func None[T any]() Option[T] {
	return Option[T]{Defined: false}
}

func (o Option[T]) IsDefined() bool {
	return o.Defined
}

func (o Option[T]) IsEmpty() bool {
	return !o.Defined
}

func (o Option[T]) Get() T {
	if !o.Defined {
		panic("Option.Get on None")
	}
	return o.Value
}

func (o Option[T]) GetOrElse(defaultValue T) T {
	if o.Defined {
		return o.Value
	}
	return defaultValue
}

func (o Option[T]) ForEach(f any) {
	if o.Defined {
		if fn, ok := f.(func(T)); ok {
			fn(o.Value)
			return
		}
		if fn, ok := f.(func(T) any); ok {
			fn(o.Value)
			return
		}

		// Fallback to reflection for cases like Option[any] with func(concreteType)
		v := reflect.ValueOf(f)
		if v.Kind() == reflect.Func && v.Type().NumIn() == 1 {
			arg := reflect.ValueOf(o.Value)
			if arg.Type().AssignableTo(v.Type().In(0)) {
				v.Call([]reflect.Value{arg})
			} else if arg.Kind() == reflect.Interface && !arg.IsNil() && arg.Elem().Type().AssignableTo(v.Type().In(0)) {
				v.Call([]reflect.Value{arg.Elem()})
			}
		}
	}
}

func (o Option[T]) Map(f any) Option[any] {
	return Map[T](o, f)
}

func (o Option[T]) FlatMap(f any) Option[any] {
	return FlatMap[T](o, f)
}

func (o Option[T]) Filter(p any) Option[T] {
	return Filter[T](o, p)
}

func ForEach[T any](o Option[T], f any) {
	o.ForEach(f)
}

func Filter[T any](o Option[T], p any) Option[T] {
	if o.Defined {
		if fn, ok := p.(func(T) bool); ok {
			if fn(o.Value) {
				return o
			}
			return None[T]()
		}
		if fn, ok := p.(func(T) any); ok {
			if fn(o.Value).(bool) {
				return o
			}
			return None[T]()
		}

		// Reflection fallback
		v := reflect.ValueOf(p)
		if v.Kind() == reflect.Func && v.Type().NumIn() == 1 && v.Type().NumOut() == 1 {
			arg := reflect.ValueOf(o.Value)
			var callArg reflect.Value
			if arg.Type().AssignableTo(v.Type().In(0)) {
				callArg = arg
			} else if arg.Kind() == reflect.Interface && !arg.IsNil() && arg.Elem().Type().AssignableTo(v.Type().In(0)) {
				callArg = arg.Elem()
			}
			if callArg.IsValid() {
				out := v.Call([]reflect.Value{callArg})
				if len(out) > 0 {
					if res, ok := out[0].Interface().(bool); ok && res {
						return o
					}
				}
			}
		}
		return None[T]()
	}
	return o
}

// Map and FlatMap are provided as functions because Go methods cannot have type parameters.
// The transpiler will rewrite o.Map(f) to std.Map(o, f).

func Map[T any](o Option[T], f any) Option[any] {
	if o.Defined {
		// Reflection fallback
		v := reflect.ValueOf(f)
		if v.Kind() == reflect.Func && v.Type().NumIn() == 1 {
			arg := reflect.ValueOf(o.Value)
			var callArg reflect.Value
			if arg.Type().AssignableTo(v.Type().In(0)) {
				callArg = arg
			} else if arg.Kind() == reflect.Interface && !arg.IsNil() && arg.Elem().Type().AssignableTo(v.Type().In(0)) {
				callArg = arg.Elem()
			}

			if callArg.IsValid() {
				out := v.Call([]reflect.Value{callArg})
				if len(out) > 0 {
					return Some(out[0].Interface())
				}
			}
		}
	}
	return None[any]()
}

func ToAnyOption(o any) Option[any] {
	if opt, ok := o.(Option[any]); ok {
		return opt
	}
	rv := reflect.ValueOf(o)
	if rv.Kind() != reflect.Struct {
		return None[any]()
	}
	definedField := rv.FieldByName("Defined")
	if !definedField.IsValid() {
		return None[any]()
	}
	if !definedField.Bool() {
		return None[any]()
	}
	valueField := rv.FieldByName("Value")
	if !valueField.IsValid() {
		return None[any]()
	}
	return Some(valueField.Interface())
}

func FlatMap[T any](o Option[T], f any) Option[any] {
	if o.Defined {
		// Reflection fallback
		v := reflect.ValueOf(f)
		if v.Kind() == reflect.Func && v.Type().NumIn() == 1 {
			arg := reflect.ValueOf(o.Value)
			var callArg reflect.Value
			if arg.Type().AssignableTo(v.Type().In(0)) {
				callArg = arg
			} else if arg.Kind() == reflect.Interface && !arg.IsNil() && arg.Elem().Type().AssignableTo(v.Type().In(0)) {
				callArg = arg.Elem()
			}

			if callArg.IsValid() {
				out := v.Call([]reflect.Value{callArg})
				if len(out) > 0 {
					return ToAnyOption(out[0].Interface())
				}
			}
		}
	}
	return None[any]()
}

func (o Option[T]) Unapply(v any) bool {
	if !o.Defined {
		return false
	}
	return UnapplyCheck(o.Value, v)
}

func (o Option[T]) Copy() Option[T] {
	if o.Defined {
		return Some(Copy(o.Value))
	}
	return None[T]()
}

func (o Option[T]) Equal(other Option[T]) bool {
	if o.Defined != other.Defined {
		return false
	}
	if !o.Defined {
		return true
	}
	return Equal(o.Value, other.Value)
}

var _ Unapply = Option[int]{}
var _ Copyable[Option[int]] = Option[int]{}
var _ Equatable[Option[int]] = Option[int]{}
