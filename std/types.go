package std

import "reflect"

// ImmutableUnwrapper is implemented by Immutable[T] to allow interface-based unwrapping.
type ImmutableUnwrapper interface {
	GetAny() any
}

func unwrapImmutable(obj any) any {
	if u, ok := obj.(ImmutableUnwrapper); ok {
		return u.GetAny()
	}
	return obj
}

func Copy[T any](v T) T {
	val := reflect.ValueOf(v)

	// Handle nil pointers early - return nil as-is
	if val.Kind() == reflect.Ptr && val.IsNil() {
		return v
	}

	// Handle nil interfaces
	if !val.IsValid() {
		return v
	}

	if c, ok := any(v).(Copyable[T]); ok {
		return c.Copy()
	}

	// Fallback to check Copy method via reflection if T is any or interface mismatch
	if val.IsValid() {
		copyMeth := val.MethodByName("Copy")
		if copyMeth.IsValid() && copyMeth.Type().NumIn() == 0 && copyMeth.Type().NumOut() == 1 {
			res := copyMeth.Call(nil)[0].Interface()
			if r, ok := res.(T); ok {
				return r
			}
		}
	}

	// For non-struct types (primitives, slices, etc.), return as-is (shallow copy)
	if val.Kind() != reflect.Struct {
		return v
	}

	newStruct := reflect.New(val.Type()).Elem()
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		newField := newStruct.Field(i)
		if newField.CanSet() {
			copiedField := Copy(field.Interface())
			newField.Set(reflect.ValueOf(copiedField))
		}
	}
	return newStruct.Interface().(T)
}

func Equal[T any](v1, v2 T) bool {
	if e, ok := any(v1).(Equatable[T]); ok {
		return e.Equal(v2)
	}

	val1 := reflect.ValueOf(v1)
	val2 := reflect.ValueOf(v2)

	// Fallback to check Equal method via reflection if T is any or interface mismatch
	if val1.IsValid() {
		equalMeth := val1.MethodByName("Equal")
		if equalMeth.IsValid() && equalMeth.Type().NumIn() == 1 && equalMeth.Type().NumOut() == 1 && equalMeth.Type().Out(0).Kind() == reflect.Bool {
			argType := equalMeth.Type().In(0)
			if val2.Type().AssignableTo(argType) {
				res := equalMeth.Call([]reflect.Value{val2})[0].Bool()
				return res
			}
		}
	}

	if val1.Kind() != reflect.Struct || val2.Kind() != reflect.Struct {
		return reflect.DeepEqual(v1, v2)
	}

	if val1.Type() != val2.Type() {
		return false
	}

	for i := 0; i < val1.NumField(); i++ {
		f1 := val1.Field(i).Interface()
		f2 := val2.Field(i).Interface()
		if !Equal(f1, f2) {
			return false
		}
	}
	return true
}

func As[T any](obj any) (T, bool) {
	// Direct type assertion
	if v, ok := obj.(T); ok {
		return v, true
	}

	// Try to unwrap if source is Immutable
	if u, ok := obj.(ImmutableUnwrapper); ok {
		unwrapped := u.GetAny()
		return As[T](unwrapped)
	}

	var zero T
	return zero, false
}
