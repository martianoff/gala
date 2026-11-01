package analyzer

import (
	"fmt"
	"go/token"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/transformer"
)

// GALA-E0055: a name spelled like a Go keyword.
//
// GALA emits every name the author wrote into the generated Go as written. The
// GALA grammar reserves some of Go's keywords itself (`func`, `type`, `if`,
// `for`, `map`, ...), so those never reach an identifier position. The rest —
// `break`, `chan`, `const`, `continue`, `default`, `defer`, `fallthrough`,
// `go`, `goto`, `select`, `switch` — lex as ordinary identifiers, and a val,
// parameter, field, function, type or import alias spelled like one produced
// Go that does not parse. The author then saw the internal GALA-E0017 ("the
// generated Go is not parseable") instead of a diagnostic about their source.
//
// Such a name is rejected, not renamed. Renaming (`go` -> `go_`) would be
// invisible for a local but not for an exported function, a struct field or a
// package-level val, which Go code, JSON field names and reflection see; and
// `break` / `continue` already mean loop control when written as a statement,
// so a binding of that name could never be referred to unambiguously. A rename
// the author chooses keeps every position consistent with no hidden mapping.
//
// Two uses are left to other checks: any use of `break` / `continue` other
// than as a declared name is loop control, which the transformer checks
// (GALA-E0059 where it cannot reach a loop), and a bare `defer`, `go`, `goto`, `fallthrough`, `select` or
// `chan` statement is GALA-E0036's, which names the GALA replacement.

// bareStatementKeywords are the keywords a bare statement may consist of
// without being a name: loop control, and GALA-E0036's statement keywords.
// Any other keyword standing alone as a statement (`switch`) is still E0055.
var bareStatementKeywords = func() map[string]bool {
	out := transformer.ForbiddenStatementKeywords()
	out["break"] = true
	out["continue"] = true
	return out
}()

// keywordName is an identifier spelled like a Go keyword, and where it starts.
type keywordName struct {
	name string
	at   transpiler.SourcePos
}

// keywordNames is what checkGoKeywordNamesNative decides about the
// identifiers it sees, in source order: the first declaration and the first
// use of a name spelled like a Go keyword.
type keywordNames struct {
	firstUse, firstDecl *keywordName
}

// done reports whether no later identifier can change the result: a
// declaration was found.
func (k *keywordNames) done() bool { return k.firstDecl != nil }

// keywordNamed reports whether name is spelled like a Go keyword: the
// identifiers keywordNames.see is told of.
func keywordNamed(name string) bool { return token.IsKeyword(name) }

// see considers the identifier name, spelled like a Go keyword
// (keywordNamed), at at. bare, decl and pattern say whether it is a whole
// statement on its own (isBareStatementNative), the name a declaration
// introduces (declaresNameNative), and part of a case pattern
// (inPatternNative); each is asked only when the answer matters.
func (k *keywordNames) see(name string, at transpiler.SourcePos, bare, decl, pattern func() bool) {
	if bareStatementKeywords[name] && bare() {
		return
	}
	isDecl := decl()
	if !isDecl && (name == "break" || name == "continue") && !pattern() {
		// Loop control in any other position (`val x = break`, an
		// if-expression branch) is the transformer's GALA-E0059,
		// which knows whether a loop can be reached from there.
		return
	}
	if isDecl {
		k.firstDecl = &keywordName{name, at}
	} else if k.firstUse == nil {
		k.firstUse = &keywordName{name, at}
	}
}

// err is the GALA-E0055 error of what k saw, preferring a declaration over a
// use, or nil when it saw neither.
func (k *keywordNames) err() error {
	id := k.firstDecl
	if id == nil {
		id = k.firstUse
	}
	if id == nil {
		return nil
	}
	name := id.name
	msg, hint := keywordUseDiagnostic(name)
	if k.firstDecl != nil {
		msg = fmt.Sprintf("%q is a Go keyword and cannot be used as a name", name)
		hint = fmt.Sprintf("rename it; GALA compiles to Go, where %q is reserved, so nothing a GALA "+
			"program declares can have that name", name)
	}
	return galaerr.NewCodedSemanticError(
		galaerr.CodeGoKeywordAsName,
		id.at.Line, id.at.Column, msg, hint,
	).WithSpan(id.at.Column + len(name))
}

// keywordUseDiagnostic words the error for a keyword used without a
// declaration — `go(f)`, `Println(default)`, a bare `switch` — where the
// author wrote Go rather than a name. A keyword GALA-E0036 knows keeps that
// check's replacement hint.
func keywordUseDiagnostic(name string) (msg, hint string) {
	msg = fmt.Sprintf("%q is a Go keyword and is not part of GALA", name)
	if suggestion, ok := transformer.ForbiddenStatementKeywordSuggestion(name); ok {
		return msg, suggestion
	}
	return msg, fmt.Sprintf("GALA has no `%s`; if this refers to something you declared, "+
		"rename that declaration", name)
}
