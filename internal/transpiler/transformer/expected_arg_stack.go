package transformer

import (
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// expectedArgTypeStack is a LIFO stack of "expected type" hints used by
// downward inference. Each enclosing context (val declaration, named-arg
// transform, tuple-element transform, etc.) that knows the expected slot
// type for the call expression about to be transformed pushes a hint;
// the call dispatcher consumes the top of the stack on entry, ensuring
// nested calls inside this one don't accidentally pick up the outer
// hint. (B1 — replaces the prior single-field `pendingExpectedArgType`
// side-channel that allowed mismatched push/pop bookkeeping to silently
// leak state across siblings.)
//
// Contract:
//   - Each push returns an unwind function that the caller must call to
//     release the slot (typically via `defer release()`). The unwind is
//     idempotent: it pops only if the hint is still on the stack — so
//     a downstream `consume()` that already removed the hint does not
//     trigger an imbalance.
//   - peek returns the top entry without removing it.
//   - consume returns and removes the top entry; nil if empty.
//   - A hint pushed for a value (pushFor) belongs to the call that value
//     is: withhold hides it while that call's receiver is lowered.
//   - The stack is per-transformer; it is not safe for concurrent use,
//     but transformers are single-threaded.
type expectedArgTypeStack struct {
	stack []expectedArg
}

// expectedArg is one hint and the postfix expression it was pushed for (nil
// when it was not pushed for one).
type expectedArg struct {
	typ   transpiler.Type
	owner *grammar.PostfixExprContext
}

// push pushes a new expected-type hint and returns an unwind function.
// The caller is expected to defer the unwind so the hint's lifetime is
// bounded by the calling scope. The unwind is idempotent — a downstream
// consume() that pops the hint first leaves the unwind as a no-op.
func (s *expectedArgTypeStack) push(t transpiler.Type) func() {
	return s.pushFor(t, nil)
}

// pushFor is push for the hint of the value owner, a postfix expression
// whose last suffix is the call that takes it (see withhold).
func (s *expectedArgTypeStack) pushFor(t transpiler.Type, owner *grammar.PostfixExprContext) func() {
	s.stack = append(s.stack, expectedArg{typ: t, owner: owner})
	pos := len(s.stack)
	return func() {
		// Only pop if our frame is still on top of the stack. If consume()
		// already removed it (or a nested push/pop pair grew and shrank
		// the stack below pos), do nothing.
		if len(s.stack) == pos {
			s.stack = s.stack[:pos-1]
		}
	}
}

// peek returns the top hint without removing it, or nil if the stack is
// empty. Use this when the caller may or may not act on the hint and the
// hint should remain visible to other readers in the same frame.
func (s *expectedArgTypeStack) peek() transpiler.Type {
	if len(s.stack) == 0 {
		return nil
	}
	return s.stack[len(s.stack)-1].typ
}

// consume returns and removes the top hint, or nil if the stack is empty.
// Use this from the call dispatcher and other "I'm acting on the hint"
// sites so the hint cannot be observed twice and nested calls inside this
// one do not pick it up.
func (s *expectedArgTypeStack) consume() transpiler.Type {
	if len(s.stack) == 0 {
		return nil
	}
	top := s.stack[len(s.stack)-1]
	s.stack = s.stack[:len(s.stack)-1]
	return top.typ
}

// withhold hides the hint on top of the stack when it was pushed for owner,
// and returns the function that puts it back, to be called once. A slot
// types the call that is its value, never that call's receiver: `parse()` in
// `parse().Get()` does not take the type `.Get()` fills.
func (s *expectedArgTypeStack) withhold(owner *grammar.PostfixExprContext) func() {
	if owner == nil || len(s.stack) == 0 || s.stack[len(s.stack)-1].owner != owner {
		return func() {}
	}
	top := s.stack[len(s.stack)-1]
	s.stack = s.stack[:len(s.stack)-1]
	return func() { s.stack = append(s.stack, top) }
}

// depth returns the current stack depth. Useful for invariant checks in
// tests (e.g. assert depth() == 0 between top-level statements).
func (s *expectedArgTypeStack) depth() int {
	return len(s.stack)
}
