package std

import "reflect"

type Int int

func (v Int) Unapply(p any) bool {
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

func (v Int8) Unapply(p any) bool {
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

func (v Int16) Unapply(p any) bool {
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

func (v Int32) Unapply(p any) bool {
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

func (v Int64) Unapply(p any) bool {
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

func (v Uint) Unapply(p any) bool {
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

func (v Uint8) Unapply(p any) bool {
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

func (v Uint16) Unapply(p any) bool {
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

func (v Uint32) Unapply(p any) bool {
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

func (v Uint64) Unapply(p any) bool {
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

func (v String) Unapply(p any) bool {
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

func (v Bool) Unapply(p any) bool {
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

func (v Float32) Unapply(p any) bool {
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

func (v Float64) Unapply(p any) bool {
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
	if u, ok := obj.(Unapply); ok {
		return u.Unapply(pattern)
	}
	return reflect.DeepEqual(obj, pattern)
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
