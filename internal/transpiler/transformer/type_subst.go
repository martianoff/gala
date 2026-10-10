package transformer

import (
	"martianoff/gala/internal/transpiler"
)

// typeSubstMap binds type parameters to the type arguments of a call: the
// receiver's, written ones, or inferred ones. It holds the types themselves,
// import paths included, so a type of a package the file does not import
// stays qualifiable wherever a parameter type is substituted.
type typeSubstMap = map[string]transpiler.Type

// anyTypeArg stands for a type parameter nothing determines: an `any`
// expected type means "infer from the body".
var anyTypeArg transpiler.Type = transpiler.BasicType{Name: "any"}
