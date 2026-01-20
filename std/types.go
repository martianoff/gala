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
