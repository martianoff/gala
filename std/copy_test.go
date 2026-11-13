package std

import (
	"fmt"
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

type anyCopyNode struct {
	calls *int
}

// Copy returns an interface, so its result type alone does not show that it
// copies the pointee rather than the pointer.
func (n anyCopyNode) Copy() any {
	*n.calls++
	return n
}

type anyCopyHolder struct {
	Node *anyCopyNode
}

// TestCopyPointerSkipsPointeeCopy guards that std.Copy leaves a pointer as it
// is without calling its pointee's value-receiver Copy, which is in the
// pointer's method set but copies the pointee. Calling it copied the
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

	anyCalls := 0
	anyNode := &anyCopyNode{calls: &anyCalls}

	assert.Same(t, anyNode, Copy[any](anyNode).(*anyCopyNode))
	assert.Same(t, anyNode, Copy(anyCopyHolder{Node: anyNode}).Node)
	assert.Equal(t, 0, anyCalls)
}

type labelCopy struct{ Label string }

// Copy returns an interface; its result is a labelCopy marked as copied.
func (c labelCopy) Copy() any { return labelCopy{Label: c.Label + " copied"} }

func (c labelCopy) String() string { return c.Label }

type strayCopy struct{ Label string }

// Copy returns a value of another type, which std.Copy must not put in place
// of a strayCopy.
func (c strayCopy) Copy() any { return "not a strayCopy" }

// TestCopyReflectedCopyResult guards which result of a Copy method found by
// reflection std.Copy returns: one its type argument accepts (the value's own
// type, or an interface it implements), and otherwise the value copied field
// by field.
func TestCopyReflectedCopyResult(t *testing.T) {
	assert.Equal(t, labelCopy{Label: "a copied"}, Copy(labelCopy{Label: "a"}))
	assert.Equal(t, labelCopy{Label: "b copied"}, Copy[fmt.Stringer](labelCopy{Label: "b"}))
	assert.Equal(t, labelCopy{Label: "c copied"}, Copy[any](labelCopy{Label: "c"}))
	assert.Equal(t, strayCopy{Label: "d"}, Copy(strayCopy{Label: "d"}))
}
