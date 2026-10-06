package std

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
)

// ImmutableUnwrapper is implemented by Immutable[T] to allow interface-based unwrapping.
type ImmutableUnwrapper interface {
	GetAny() any
}

// A GALA struct field or package-level val that is not `var` is an
// Immutable[T] in Go, and the value inside it is unexported, so Go's
// reflection-based encoders and fmt would see an empty struct. These methods
// make Immutable[T] read and write as the value it holds: in encoding/json,
// in YAML libraries that use the MarshalYAML / UnmarshalYAML(func(any) error)
// convention (gopkg.in/yaml.v2 and v3), and in fmt. A struct tag on the field
// applies to the wrapped value.
var (
	_ json.Marshaler   = Immutable[int]{}
	_ json.Unmarshaler = (*Immutable[int])(nil)
	_ fmt.Formatter    = Immutable[int]{}
)

// MarshalJSON encodes the wrapped value through a pointer, so T's
// pointer-receiver MarshalJSON applies too. HTML escaping is left to the
// calling encoder, which applies its own setting to the result.
func (i Immutable[T]) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(&i.value); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UnmarshalJSON decodes into the wrapped value. Like any json.Unmarshaler,
// it does not see the calling Decoder's settings (DisallowUnknownFields,
// UseNumber).
func (i *Immutable[T]) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &i.value)
}

// IsZero reports whether the wrapped value is its type's zero value. YAML
// encoders use it for `omitempty`, and encoding/json for `omitzero`; without
// it they would judge the wrapper by its exported fields, of which it has
// none.
func (i Immutable[T]) IsZero() bool {
	return reflect.ValueOf(&i.value).Elem().IsZero()
}

// MarshalYAML returns the wrapped value for the YAML encoder to encode.
func (i Immutable[T]) MarshalYAML() (any, error) {
	return i.value, nil
}

// UnmarshalYAML decodes into the wrapped value.
func (i *Immutable[T]) UnmarshalYAML(unmarshal func(any) error) error {
	return unmarshal(&i.value)
}

// Format prints the wrapped value with the same verb, flags, width and
// precision.
func (i Immutable[T]) Format(f fmt.State, verb rune) {
	fmt.Fprintf(f, fmt.FormatString(f, verb), i.value)
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
	v1, v2 = unwrapPanic(v1), unwrapPanic(v2)
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

// tryRecover executes f with panic recovery, returning a Try.
// Used by TryApply (defined in try.gala) as the underlying implementation
// since GALA cannot express Go's defer/recover with named return values.
//
// A recovered panic becomes a Failure holding a *panicError, which records the
// stack of the panic. The stack is taken inside the deferred recover, while the
// panicking frames are still on the goroutine's stack; nothing is captured on
// the success path.
func tryRecover[T any](f func() T) (result Try[T]) {
	defer func() {
		if r := recover(); r != nil {
			result = Try[T]{Err: NewImmutable(recoveredError(r)), _variant: _Try_Failure}
		}
	}()
	v := f()
	result = Try[T]{Value: NewImmutable(v), _variant: _Try_Success}
	return
}

// recoveredError turns a recovered panic value into the error a Failure holds.
// It must be called from the deferred function that recovered r, so the stack
// it records still contains the panicking frames. A panic that re-raises a
// recovered panic's error (Try.Get inside an outer Try) keeps the original
// stack, which names where the problem started.
func recoveredError(r any) error {
	var err error
	switch e := r.(type) {
	case *panicError:
		return e
	case error:
		err = e
	case string:
		err = errors.New(e)
	default:
		err = fmt.Errorf("panic: %v", r)
	}
	// Only the program counters are recorded here: a Failure is often handled
	// without anyone reading its stack, so symbolizing waits for PanicStack.
	var pcs [maxPanicFrames]uintptr
	n := runtime.Callers(2, pcs[:])
	return &panicError{err: err, pcs: append([]uintptr(nil), pcs[:n]...)}
}

// maxPanicFrames bounds the frames kept for a recovered panic. The innermost
// frames, the ones that locate the panic, are the ones kept.
const maxPanicFrames = 64

// panicError is the error of a Failure produced by a recovered panic. It is
// transparent: Error() and fmt formatting are the panic's own, Unwrap returns
// the panic's error (so errors.Is / errors.As see it), and Equal and As (hence
// GALA type patterns) look through it. Only PanicStack reads the recorded
// stack, so two Failures of the same panic value stay equal and printing a
// Failure does not dump a stack.
type panicError struct {
	err error
	pcs []uintptr
}

func (e *panicError) Error() string { return e.err.Error() }

func (e *panicError) Unwrap() error { return e.err }

// Format prints the panic's error exactly as it prints on its own.
func (e *panicError) Format(s fmt.State, verb rune) {
	fmt.Fprintf(s, fmt.FormatString(s, verb), e.err)
}

// stack renders the recorded frames as a Go traceback lists them, a function
// and its file:line per frame, starting at the frame that panicked: the frames
// between the recover and the panic (up to runtime.gopanic) are left out.
func (e *panicError) stack() string {
	var b strings.Builder
	frames := runtime.CallersFrames(e.pcs)
	started := false
	for more := true; more; {
		var f runtime.Frame
		f, more = frames.Next()
		if started {
			fmt.Fprintf(&b, "%s(...)\n\t%s:%d\n", f.Function, f.File, f.Line)
		}
		started = started || f.Function == "runtime.gopanic"
	}
	return b.String()
}

// panicStackOf returns the stack recorded for the panic err came from, or None
// when neither err nor any error it wraps is a recovered panic. PanicStack
// (try.gala) is its GALA face.
func panicStackOf(err error) Option[string] {
	var p *panicError
	if errors.As(err, &p) {
		return Some[string]{}.Apply(p.stack())
	}
	return None[string]{}.Apply()
}

// unwrapPanic returns the panic's own error when v is a *panicError whose
// error is a T, and v unchanged otherwise.
func unwrapPanic[T any](v T) T {
	if p, ok := any(v).(*panicError); ok {
		if inner, ok := any(p.err).(T); ok {
			return inner
		}
	}
	return v
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

	// A recovered panic matches the patterns its own error matches
	if p, ok := obj.(*panicError); ok {
		return As[T](p.err)
	}

	var zero T
	return zero, false
}
