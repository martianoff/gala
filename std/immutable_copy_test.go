package std

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type immCopyCounter struct {
	calls *int
}

// Copy counts its calls, so a test can tell whether std.Copy invoked it.
func (c immCopyCounter) Copy() immCopyCounter {
	*c.calls++
	return c
}

// TestCopyImmutableSharesValue guards that std.Copy of an Immutable returns it
// as it is, without copying the value it holds: nothing can change that value.
func TestCopyImmutableSharesValue(t *testing.T) {
	calls := 0
	imm := NewImmutable(immCopyCounter{calls: &calls})

	assert.Same(t, &calls, Copy(imm).Get().calls)
	assert.Same(t, &calls, Copy[any](imm).(Immutable[immCopyCounter]).Get().calls)
	assert.Equal(t, 0, calls, "the held value's Copy must not run")
}
