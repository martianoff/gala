package std

import "reflect"
import "strings"
import "unsafe"

type Int int

func (v Int) Unapply(p any) any {
	return reflect.DeepEqual(int(v), p)
}

func (v Int) Copy() Int {
	return v
}

func (v Int) Equal(other Int) bool {
	return v == other
}

var _ Copyable[Int] = (Int)(0)
var _ Equatable[Int] = (Int)(0)

type Int8 int8

func (v Int8) Unapply(p any) any {
	return reflect.DeepEqual(int8(v), p)
}

func (v Int8) Copy() Int8 {
	return v
}

func (v Int8) Equal(other Int8) bool {
	return v == other
}

var _ Copyable[Int8] = (Int8)(0)
var _ Equatable[Int8] = (Int8)(0)

type Int16 int16

func (v Int16) Unapply(p any) any {
	return reflect.DeepEqual(int16(v), p)
}

func (v Int16) Copy() Int16 {
	return v
}

func (v Int16) Equal(other Int16) bool {
	return v == other
}

var _ Copyable[Int16] = (Int16)(0)
var _ Equatable[Int16] = (Int16)(0)

type Int32 int32

func (v Int32) Unapply(p any) any {
	return reflect.DeepEqual(int32(v), p)
}

func (v Int32) Copy() Int32 {
	return v
}

func (v Int32) Equal(other Int32) bool {
	return v == other
}

var _ Copyable[Int32] = (Int32)(0)
var _ Equatable[Int32] = (Int32)(0)

type Int64 int64

func (v Int64) Unapply(p any) any {
	return reflect.DeepEqual(int64(v), p)
}

func (v Int64) Copy() Int64 {
	return v
}

func (v Int64) Equal(other Int64) bool {
	return v == other
}

var _ Copyable[Int64] = (Int64)(0)
var _ Equatable[Int64] = (Int64)(0)

type Uint uint

func (v Uint) Unapply(p any) any {
	return reflect.DeepEqual(uint(v), p)
}

func (v Uint) Copy() Uint {
	return v
}

func (v Uint) Equal(other Uint) bool {
	return v == other
}

var _ Copyable[Uint] = (Uint)(0)
var _ Equatable[Uint] = (Uint)(0)

type Uint8 uint8

func (v Uint8) Unapply(p any) any {
	return reflect.DeepEqual(uint8(v), p)
}

func (v Uint8) Copy() Uint8 {
	return v
}

func (v Uint8) Equal(other Uint8) bool {
	return v == other
}

var _ Copyable[Uint8] = (Uint8)(0)
var _ Equatable[Uint8] = (Uint8)(0)

type Uint16 uint16

func (v Uint16) Unapply(p any) any {
	return reflect.DeepEqual(uint16(v), p)
}

func (v Uint16) Copy() Uint16 {
	return v
}

func (v Uint16) Equal(other Uint16) bool {
	return v == other
}

var _ Copyable[Uint16] = (Uint16)(0)
var _ Equatable[Uint16] = (Uint16)(0)

type Uint32 uint32

func (v Uint32) Unapply(p any) any {
	return reflect.DeepEqual(uint32(v), p)
}

func (v Uint32) Copy() Uint32 {
	return v
}

func (v Uint32) Equal(other Uint32) bool {
	return v == other
}

var _ Copyable[Uint32] = (Uint32)(0)
var _ Equatable[Uint32] = (Uint32)(0)

type Uint64 uint64

func (v Uint64) Unapply(p any) any {
	return reflect.DeepEqual(uint64(v), p)
}

func (v Uint64) Copy() Uint64 {
	return v
}

func (v Uint64) Equal(other Uint64) bool {
	return v == other
}

var _ Copyable[Uint64] = (Uint64)(0)
var _ Equatable[Uint64] = (Uint64)(0)

type String string

func (v String) Unapply(p any) any {
	return reflect.DeepEqual(string(v), p)
}

func (v String) Copy() String {
	return v
}

func (v String) Equal(other String) bool {
	return v == other
}

var _ Copyable[String] = (String)("")
var _ Equatable[String] = (String)("")

type Bool bool

func (v Bool) Unapply(p any) any {
	return reflect.DeepEqual(bool(v), p)
}

func (v Bool) Copy() Bool {
	return v
}

func (v Bool) Equal(other Bool) bool {
	return v == other
}

var _ Copyable[Bool] = (Bool)(false)
var _ Equatable[Bool] = (Bool)(false)

type Float32 float32

func (v Float32) Unapply(p any) any {
	return reflect.DeepEqual(float32(v), p)
}

func (v Float32) Copy() Float32 {
	return v
}

func (v Float32) Equal(other Float32) bool {
	return v == other
}

var _ Copyable[Float32] = (Float32)(0)
var _ Equatable[Float32] = (Float32)(0)

type Float64 float64

func (v Float64) Unapply(p any) any {
	return reflect.DeepEqual(float64(v), p)
}

func (v Float64) Copy() Float64 {
	return v
}

func (v Float64) Equal(other Float64) bool {
	return v == other
}

var _ Copyable[Float64] = (Float64)(0)
var _ Equatable[Float64] = (Float64)(0)

func UnapplyCheck(obj any, pattern any) bool {
	_, ok := UnapplyFull(obj, pattern)
	return ok
}

func UnapplyFull(obj any, pattern any) ([]any, bool) {
	obj = unwrapImmutable(obj)

	if u, ok := pattern.(Unapply); ok {
		res := u.Unapply(obj)
		if isDefined(res) {
			val := getSomeValue(res)
			return []any{val}, true
		}
		return nil, false
	}

	// Also try pattern.Unapply(obj) via reflection if interface not satisfied
	patVal := reflect.ValueOf(pattern)
	if !patVal.IsValid() {
		return nil, false
	}
	unapplyMeth := patVal.MethodByName("Unapply")
	if unapplyMeth.IsValid() && unapplyMeth.Type().NumIn() == 1 {
		// Call it with obj. Handle nil obj by using zero value of the expected type.
		argVal := reflect.ValueOf(obj)
		if !argVal.IsValid() {
			argVal = reflect.Zero(unapplyMeth.Type().In(0))
		} else if argVal.Type() != unapplyMeth.Type().In(0) && !argVal.Type().AssignableTo(unapplyMeth.Type().In(0)) {
			// Try to convert if possible, or return false if types are incompatible
			if argVal.Type().ConvertibleTo(unapplyMeth.Type().In(0)) {
				argVal = argVal.Convert(unapplyMeth.Type().In(0))
			} else {
				return nil, false
			}
		}

		resVals := unapplyMeth.Call([]reflect.Value{argVal})
		if len(resVals) > 0 {
			// Check if last return value is bool (positional extraction)
			lastIdx := len(resVals) - 1
			if resVals[lastIdx].Kind() == reflect.Bool {
				if !resVals[lastIdx].Bool() {
					return nil, false
				}
				var results []any
				for i := 0; i < lastIdx; i++ {
					results = append(results, resVals[i].Interface())
				}
				return results, true
			}

			// Handle Option-style (single return value)
			res := resVals[0].Interface()
			if isDefined(res) {
				return []any{getSomeValue(res)}, true
			}
			return nil, false
		}
	}

	if reflect.DeepEqual(obj, pattern) {
		return []any{obj}, true
	}
	return nil, false
}

func UnapplyTuple(obj any) ([]any, bool) {
	obj = unwrapImmutable(obj)
	if obj == nil {
		return nil, false
	}
	v := reflect.ValueOf(obj)
	if v.Kind() == reflect.Struct && (strings.Contains(v.Type().Name(), "Tuple") || strings.Contains(v.Type().String(), "Tuple")) {
		f1 := v.FieldByName("V1")
		f2 := v.FieldByName("V2")
		if f1.IsValid() && f2.IsValid() {
			return []any{unwrapImmutable(f1.Interface()), unwrapImmutable(f2.Interface())}, true
		}
	}
	return nil, false
}

func GetSafe(res []any, i int) any {
	if i < 0 || i >= len(res) {
		return nil
	}
	return res[i]
}

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
