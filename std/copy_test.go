package std

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type copyNode struct {
	calls *int
}

// Copy counts its calls, so a test can tell whether std.Copy invoked it.
func (n copyNode) Copy() copyNode {
	*n.calls++
	return n
}

type copyHolder struct {
	Node *copyNode
}

// TestCopyPointerSkipsPointeeCopy guards that std.Copy leaves a pointer as it
// is without calling its pointee's value-receiver Copy, which is in the
// pointer's method set but returns the pointee's type. Calling it copied the
// whole pointee — every node of a linked structure — only to drop the result,
// and in a struct without a Copy of its own it tried to store the pointee in
// the pointer field, which panicked.
func TestCopyPointerSkipsPointeeCopy(t *testing.T) {
	calls := 0
	node := &copyNode{calls: &calls}

	assert.Same(t, node, Copy(node))
	assert.Same(t, node, Copy[any](node).(*copyNode))
	assert.Same(t, node, Copy(copyHolder{Node: node}).Node)
	assert.Equal(t, 0, calls)

	assert.Equal(t, *node, Copy(*node))
	assert.Equal(t, 1, calls)
}

// TestCopyImmutableSharesValue guards that std.Copy of an Immutable returns it
// as it is, without copying the value it holds: nothing can change that value.
func TestCopyImmutableSharesValue(t *testing.T) {
	calls := 0
	imm := NewImmutable(copyNode{calls: &calls})

	assert.Same(t, &calls, Copy(imm).Get().calls)
	assert.Same(t, &calls, Copy[any](imm).(Immutable[copyNode]).Get().calls)
	assert.Equal(t, 0, calls, "the held value's Copy must not run")
}
