package gala2lean

// Names. A GALA identifier keeps its spelling in Lean unless Lean would read
// it differently: a Lean keyword is written «x», and a name the generated
// code or Lean itself relies on (pure, get, Int64, a constructor's rec, ...)
// gets a trailing prime, x'. A GALA identifier never contains a prime, so the
// renamed one cannot collide with another. The translator's own names start
// with `_gala_`, which GALA reserves for generated code.

// leanKeywords are Lean 4 keywords and tokens that cannot be identifiers.
var leanKeywords = map[string]bool{
	"abbrev": true, "at": true, "attribute": true, "axiom": true, "by": true, "calc": true,
	"catch": true, "class": true, "deriving": true, "def": true, "do": true, "else": true,
	"end": true, "example": true, "export": true, "extends": true, "finally": true,
	"for": true, "from": true, "fun": true, "have": true, "if": true, "import": true,
	"in": true, "inductive": true, "infix": true, "infixl": true, "infixr": true,
	"instance": true, "let": true, "local": true, "macro": true, "match": true,
	"mut": true, "mutual": true, "namespace": true, "nomatch": true, "nofun": true,
	"noncomputable": true, "notation": true, "open": true, "opaque": true,
	"partial": true, "postfix": true, "prefix": true, "private": true, "protected": true,
	"public": true, "return": true, "section": true, "set_option": true,
	"show": true, "structure": true, "suffices": true, "syntax": true, "then": true,
	"theorem": true, "this": true, "try": true, "unless": true, "universe": true,
	"unsafe": true, "using": true, "variable": true, "where": true, "with": true,
	"Type": true, "Prop": true, "Sort": true, "break": true, "continue": true,
	"termination_by": true, "decreasing_by": true, "partial_fixpoint": true,
	"omit": true, "include": true, "builtin_initialize": true, "initialize": true,
	"elab": true, "if_let": true, "fun_prop": true, "λ": true, "deriving_instance": true,
}

// leanReserved are names the generated code uses unqualified, and names Lean
// declares for every structure or inductive (S.mk, S.rec, ...).
var leanReserved = map[string]bool{
	// The monad and do-notation.
	"pure": true, "bind": true, "get": true, "set": true, "modify": true, "throw": true,
	"discard": true, "decide": true, "default": true, "true": true, "false": true,
	"id": true, "some": true, "none": true, "not": true, "and": true, "or": true,
	// Lean types the translation names.
	"Int64": true, "String": true, "Bool": true, "Unit": true, "Option": true,
	"Nat": true, "Int": true, "IO": true, "UInt32": true, "List": true, "Except": true,
	"ExceptT": true, "StateT": true, "Functor": true, "Monad": true, "BEq": true,
	"Inhabited": true, "HAdd": true, "Gala": true,
	// The prelude (namespace Gala.Gen, opened by every program).
	"Panic": true, "Prog": true, "HasOut": true, "GShow": true, "GAdd": true,
	"gshow": true, "graw": true, "gadd": true, "gdiv": true, "gmod": true, "gshl": true,
	"gshr": true, "gprintln": true, "gprint": true, "garg": true, "emit": true,
	"wrapImm": true, "printText": true, "runMain": true, "outcomeLine": true,
	"Arg": true, "T2": true, "T3": true, "T4": true,
	// Declarations Lean adds to every structure and inductive.
	"mk": true, "rec": true, "casesOn": true, "recOn": true, "noConfusion": true,
	"noConfusionType": true, "below": true, "brecOn": true, "binductionOn": true,
	"ibelow": true, "ctorIdx": true, "toCtorIdx": true, "injEq": true, "inj": true,
	"sizeOf_spec": true, "ext": true, "ext_iff": true, "eta": true,
}

// ident is GALA identifier x as a Lean identifier.
func ident(x string) string {
	switch {
	case leanKeywords[x]:
		return "«" + x + "»"
	case leanReserved[x]:
		return x + "'"
	}
	return x
}
