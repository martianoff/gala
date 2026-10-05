package std

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

type stackTestError struct{ Code int }

func (e stackTestError) Error() string { return fmt.Sprintf("code %d", e.Code) }

func panicsWithString() int { panic("boom") }

func panicsWithError() int { panic(stackTestError{Code: 7}) }

func panicsWithValue() int { panic(42) }

func TestTryRecover_PanicKeepsStack(t *testing.T) {
	tests := []struct {
		name     string
		f        func() int
		message  string
		function string
	}{
		{"string panic", panicsWithString, "boom", "std.panicsWithString"},
		{"error panic", panicsWithError, "code 7", "std.panicsWithError"},
		{"other value panic", panicsWithValue, "panic: 42", "std.panicsWithValue"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tryRecover(tt.f).GetError()
			assert.Equal(t, tt.message, err.Error(), "Error() is the panic's own message")
			stack := PanicStack(err)
			assert.True(t, stack.IsDefined())
			assert.Contains(t, stack.Get(), tt.function, "the stack names the panicking frame")
		})
	}
}

func TestPanicStack_NoneForOrdinaryErrors(t *testing.T) {
	assert.False(t, PanicStack(errors.New("plain")).IsDefined())
	assert.False(t, PanicStack(FromError(io.EOF).GetError()).IsDefined())
	assert.False(t, PanicStack(nil).IsDefined())
}

func TestPanicStack_FoundThroughWrapping(t *testing.T) {
	err := tryRecover(panicsWithString).GetError()
	wrapped := fmt.Errorf("handler: %w", err)
	assert.Equal(t, PanicStack(err), PanicStack(wrapped))
}

func TestTryRecover_RepanicKeepsOriginalStack(t *testing.T) {
	inner := tryRecover(panicsWithError)
	outer := tryRecover(func() int { return inner.Get() })
	assert.Same(t, inner.GetError(), outer.GetError())
}

func TestPanicError_IsTransparent(t *testing.T) {
	err := tryRecover(panicsWithError).GetError()

	var target stackTestError
	assert.True(t, errors.As(err, &target))
	assert.Equal(t, 7, target.Code)
	assert.True(t, errors.Is(err, stackTestError{Code: 7}))

	matched, ok := As[stackTestError](err)
	assert.True(t, ok, "type patterns see the panic's own error")
	assert.Equal(t, 7, matched.Code)
	asErr, ok := As[error](err)
	assert.True(t, ok)
	assert.Same(t, err, asErr)

	for _, verb := range []string{"%v", "%s", "%+v", "%q", "%#v"} {
		assert.Equal(t, fmt.Sprintf(verb, stackTestError{Code: 7}), fmt.Sprintf(verb, err), verb)
	}
	assert.False(t, strings.Contains(fmt.Sprint(tryRecover(panicsWithString)), "goroutine"),
		"printing a Failure does not dump the stack")
}

func TestPanicError_EqualityIgnoresStack(t *testing.T) {
	first := tryRecover(panicsWithString)
	second := tryRecover(func() int { panic("boom") })
	plain := Failure[int]{}.Apply(errors.New("boom"))
	other := tryRecover(func() int { panic("other") })

	assert.True(t, first.Equal(second))
	assert.True(t, first.Equal(plain))
	assert.True(t, plain.Equal(first))
	assert.False(t, first.Equal(other))
	assert.False(t, other.Equal(first))
}
