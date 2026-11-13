package transformer

import (
	"fmt"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// A type's own Equal that hands its own type to a generic function as a
// type argument constrained by comparable recurses forever: `==` on that
// type parameter lowers to std.EqualComparable, which calls the value's
// Equal, which calls the function again. `==` written in Equal itself is
// Go's (see equalityOf); a function it calls is compiled once for every
// caller, so it cannot be told apart there. The call is rejected instead
// (GALA-E0108).

// checkEqualReentry rejects, inside the Equal method of a GALA type, a call
// of the generic function or method name whose type parameter, constrained
// by comparable or by a Go interface that embeds it, takes as its type
// argument the type Equal is defined on, or a type that holds it by value
// (Option[T], a tuple, a List[T]): its == would call Equal again. bound maps
// typeParams to the call's type arguments, as far as they are known.
func (t *galaASTTransformer) checkEqualReentry(c callSrc, name string, typeParams []string, constraints map[string]transpiler.Type, bound typeSubstMap) error {
	if t.equalOwner == nil {
		return nil
	}
	// == calls only an Equal(T) bool.
	if _, usable := t.equalMethodOf(t.equalOwner); !usable {
		return nil
	}
	for _, tp := range typeParams {
		arg := bound[tp]
		if arg == nil || transpiler.IsUnusableOrAny(arg) || !t.isComparableConstraint(constraints[tp]) {
			continue
		}
		// == on arg would call the Equal being transformed: arg is its
		// type, or holds it by value.
		if walk := (&comparer{t: t, exhaustive: true, equalCalls: true}).walk(arg, nil, false); !walk.holdsOwner {
			continue
		}
		r := c.anchor().rng()
		owner := t.equalOwner.Name
		return spanErrorAt(galaerr.CodeEqualReentry, r, r,
			fmt.Sprintf("%s's own Equal calls %s with %s = %s, a comparable type parameter: == on %s there calls %s's Equal, so the two call each other forever",
				owner, name, tp, displayType(arg), tp, owner),
			fmt.Sprintf("compare %s's fields instead", owner))
	}
	return nil
}
