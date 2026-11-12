package transpiler

import "strings"

// ConstraintIs reports whether c, a type parameter's constraint, is the
// predeclared constraint name ("any" or "comparable"). A nil constraint is
// `any`: a parameter declared without one, or one metadata did not record.
func ConstraintIs(c Type, name string) bool {
	if c == nil || c.IsNil() {
		return name == "any"
	}
	switch c := c.(type) {
	case BasicType:
		return c.Name == name
	case NamedType:
		return c.Package == "" && c.Name == name
	}
	return false
}

// IsInterfaceConstraint reports whether c, a type parameter's constraint, is
// neither `any` nor `comparable`: a named interface, generic or not, whose
// methods a value of the parameter's type has.
func IsInterfaceConstraint(c Type) bool {
	return !ConstraintIs(c, "any") && !ConstraintIs(c, "comparable")
}

// TypeParamList spells the type parameter list params declares with their
// constraints: `T any, C Iterable[T, C]`.
func TypeParamList(params []string, constraints map[string]Type) string {
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = p + " " + ConstraintString(constraints[p])
	}
	return strings.Join(parts, ", ")
}

// ConstraintString spells the constraint c, `any` when there is none.
func ConstraintString(c Type) string {
	if c == nil || c.IsNil() {
		return "any"
	}
	return c.String()
}
