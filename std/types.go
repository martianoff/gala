package std

import "reflect"
import "strings"
import "unsafe"

func unwrapImmutable(obj any) any {
	if obj == nil {
		return nil
	}
	v := reflect.ValueOf(obj)
	if v.Kind() == reflect.Struct {
		// Only unwrap if it's actually an Immutable struct.
		// Check type name because it might be generic (e.g. Immutable[int])
		typeName := v.Type().Name()
		if typeName == "" {
			// Might be a generic instantiation, check the string representation
			s := v.Type().String()
			if !strings.Contains(s, "Immutable") {
				return obj
			}
		} else if !strings.Contains(typeName, "Immutable") {
			return obj
		}

		meth := v.MethodByName("Get")
		if meth.IsValid() {
			res := meth.Call(nil)
			if len(res) > 0 {
				return res[0].Interface()
			}
		}
	}
	return obj
}

func Copy[T any](v T) T {
	if c, ok := any(v).(Copyable[T]); ok {
		return c.Copy()
	}

	val := reflect.ValueOf(v)
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
	res, ok := asInternal(obj, reflect.TypeOf((*T)(nil)).Elem())
	if !ok {
		return *new(T), false
	}
	return res.(T), true
}

func asInternal(obj any, targetType reflect.Type) (any, bool) {
	if obj == nil {
		return nil, false
	}

	v := reflect.ValueOf(obj)
	if v.Type().AssignableTo(targetType) {
		return obj, true
	}

	// Try to unwrap if target is not Immutable but source is
	if !strings.Contains(targetType.String(), "Immutable") && strings.Contains(v.Type().String(), "Immutable") {
		unwrapped := unwrapImmutable(obj)
		return asInternal(unwrapped, targetType)
	}

	if v.Kind() != reflect.Struct || targetType.Kind() != reflect.Struct {
		return nil, false
	}

	// Make addressable copy to access unexported fields
	vAddr := v
	if !v.CanAddr() {
		vAddr = reflect.New(v.Type()).Elem()
		vAddr.Set(v)
	}

	// Handle Immutable specifically
	if strings.Contains(v.Type().String(), "Immutable") && strings.Contains(targetType.String(), "Immutable") {
		meth := v.MethodByName("Get")
		if meth.IsValid() {
			innerVal := meth.Call(nil)[0].Interface()
			newImm := reflect.New(targetType).Elem()
			targetInnerType := targetType.Field(0).Type
			convertedInner, ok := asInternal(innerVal, targetInnerType)
			if !ok {
				return nil, false
			}
			setUnexportedField(newImm.Field(0), reflect.ValueOf(convertedInner))
			return newImm.Interface(), true
		}
	}

	if targetType.NumField() != v.NumField() {
		return nil, false
	}

	for i := 0; i < targetType.NumField(); i++ {
		if targetType.Field(i).Name != v.Type().Field(i).Name {
			return nil, false
		}
	}

	newTarget := reflect.New(targetType).Elem()
	for i := 0; i < targetType.NumField(); i++ {
		srcField := vAddr.Field(i)
		targetField := newTarget.Field(i)

		srcFieldVal := srcField
		if !srcFieldVal.CanInterface() {
			srcFieldVal = reflect.NewAt(srcField.Type(), unsafe.Pointer(srcField.UnsafeAddr())).Elem()
		}

		converted, ok := asInternal(srcFieldVal.Interface(), targetField.Type())
		if !ok {
			return nil, false
		}

		valToSet := reflect.ValueOf(converted)
		if targetField.CanSet() {
			targetField.Set(valToSet)
		} else {
			setUnexportedField(targetField, valToSet)
		}
	}

	return newTarget.Interface(), true
}

func setUnexportedField(field reflect.Value, value reflect.Value) {
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(value)
}

// === Slice Helper Functions for efficient operations ===

// SliceAppendAll appends all elements from src to dst. O(m) where m = len(src).
func SliceAppendAll[T any](dst []T, src []T) []T {
	return append(dst, src...)
}

// SlicePrepend inserts a value at the front of a slice. O(n).
// Uses in-place shift for efficiency.
func SlicePrepend[T any](s []T, value T) []T {
	s = append(s, value)
	copy(s[1:], s[:len(s)-1])
	s[0] = value
	return s
}

// SlicePrependAll prepends all elements from values to s. O(n+m).
func SlicePrependAll[T any](s []T, values []T) []T {
	if len(values) == 0 {
		return s
	}
	result := make([]T, len(s)+len(values))
	copy(result, values)
	copy(result[len(values):], s)
	return result
}

// SliceInsert inserts a value at the given index. O(n).
func SliceInsert[T any](s []T, index int, value T) []T {
	var zero T
	s = append(s, zero)
	copy(s[index+1:], s[index:len(s)-1])
	s[index] = value
	return s
}

// SliceRemoveAt removes the element at the given index. O(n).
func SliceRemoveAt[T any](s []T, index int) []T {
	copy(s[index:], s[index+1:])
	return s[:len(s)-1]
}

// SliceDrop returns a slice with the first n elements removed. O(1).
func SliceDrop[T any](s []T, n int) []T {
	if n >= len(s) {
		return nil
	}
	return s[n:]
}

// SliceTake returns a slice with only the first n elements. O(1).
func SliceTake[T any](s []T, n int) []T {
	if n >= len(s) {
		return s
	}
	return s[:n]
}

// === Slice Creation Functions ===

// SliceEmpty creates an empty slice of type T.
func SliceEmpty[T any]() []T {
	return nil
}

// SliceOf creates a slice from variadic arguments.
func SliceOf[T any](elements ...T) []T {
	return elements
}

// SliceWithCapacity creates an empty slice with pre-allocated capacity.
func SliceWithCapacity[T any](capacity int) []T {
	return make([]T, 0, capacity)
}

// SliceWithSize creates a slice with specified length (zero-initialized).
func SliceWithSize[T any](size int) []T {
	return make([]T, size)
}

// SliceWithSizeAndCapacity creates a slice with specified length and capacity.
func SliceWithSizeAndCapacity[T any](size int, capacity int) []T {
	return make([]T, size, capacity)
}

// SliceCopy creates a copy of an existing slice.
func SliceCopy[T any](elements []T) []T {
	if elements == nil {
		return nil
	}
	result := make([]T, len(elements))
	copy(result, elements)
	return result
}

// === Map Creation Functions ===

// MapEmpty creates an empty map of type map[K]V.
func MapEmpty[K comparable, V any]() map[K]V {
	return make(map[K]V)
}

// MapWithCapacity creates an empty map with pre-allocated capacity.
func MapWithCapacity[K comparable, V any](capacity int) map[K]V {
	return make(map[K]V, capacity)
}

// === Map Mutation Functions ===

// MapPut adds or updates a key-value pair. Returns the map for chaining.
func MapPut[K comparable, V any](m map[K]V, k K, v V) map[K]V {
	m[k] = v
	return m
}

// MapDelete removes a key. Returns the map for chaining.
func MapDelete[K comparable, V any](m map[K]V, k K) map[K]V {
	delete(m, k)
	return m
}

// === Map Query Functions ===

// MapGet returns the value for a key and whether it exists.
func MapGet[K comparable, V any](m map[K]V, k K) (V, bool) {
	v, ok := m[k]
	return v, ok
}

// MapContains checks if a key exists.
func MapContains[K comparable, V any](m map[K]V, k K) bool {
	_, ok := m[k]
	return ok
}

// MapLen returns the number of entries.
func MapLen[K comparable, V any](m map[K]V) int {
	return len(m)
}

// === Map Iteration Functions ===

// MapForEach applies a function to each key-value pair.
func MapForEach[K comparable, V any](m map[K]V, f func(K, V)) {
	for k, v := range m {
		f(k, v)
	}
}

// MapKeys returns a slice of all keys.
func MapKeys[K comparable, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// MapValues returns a slice of all values.
func MapValues[K comparable, V any](m map[K]V) []V {
	values := make([]V, 0, len(m))
	for _, v := range m {
		values = append(values, v)
	}
	return values
}

// === Map Copy Function ===

// MapCopy creates a shallow copy of a map.
func MapCopy[K comparable, V any](m map[K]V) map[K]V {
	if m == nil {
		return nil
	}
	result := make(map[K]V, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}
