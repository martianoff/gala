package std

import "reflect"

type Int int

func (v Int) Unapply(p any) bool {
	return reflect.DeepEqual(int(v), p)
}

type Int8 int8

func (v Int8) Unapply(p any) bool {
	return reflect.DeepEqual(int8(v), p)
}

type Int16 int16

func (v Int16) Unapply(p any) bool {
	return reflect.DeepEqual(int16(v), p)
}

type Int32 int32

func (v Int32) Unapply(p any) bool {
	return reflect.DeepEqual(int32(v), p)
}

type Int64 int64

func (v Int64) Unapply(p any) bool {
	return reflect.DeepEqual(int64(v), p)
}

type Uint uint

func (v Uint) Unapply(p any) bool {
	return reflect.DeepEqual(uint(v), p)
}

type Uint8 uint8

func (v Uint8) Unapply(p any) bool {
	return reflect.DeepEqual(uint8(v), p)
}

type Uint16 uint16

func (v Uint16) Unapply(p any) bool {
	return reflect.DeepEqual(uint16(v), p)
}

type Uint32 uint32

func (v Uint32) Unapply(p any) bool {
	return reflect.DeepEqual(uint32(v), p)
}

type Uint64 uint64

func (v Uint64) Unapply(p any) bool {
	return reflect.DeepEqual(uint64(v), p)
}

type String string

func (v String) Unapply(p any) bool {
	return reflect.DeepEqual(string(v), p)
}

type Bool bool

func (v Bool) Unapply(p any) bool {
	return reflect.DeepEqual(bool(v), p)
}

type Float32 float32

func (v Float32) Unapply(p any) bool {
	return reflect.DeepEqual(float32(v), p)
}

type Float64 float64

func (v Float64) Unapply(p any) bool {
	return reflect.DeepEqual(float64(v), p)
}

func UnapplyCheck(obj any, pattern any) bool {
	if u, ok := obj.(Unapply); ok {
		return u.Unapply(pattern)
	}
	return reflect.DeepEqual(obj, pattern)
}
