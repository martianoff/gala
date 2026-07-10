package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestForbiddenStatementKeywordsAreHardErrors verifies that the Go-only
// statement keywords GALA does not have in its grammar (defer, go, goto,
// fallthrough, select, chan) are rejected with GALA-E0036 when they appear as
// a bare identifier statement. Historically these "worked" only as an accident
// of the final gofmt pass re-absorbing the following statement (e.g. `defer` +
// `f.Close()` gluing into a Go DeferStmt); forbidding them removes that silent
// Go leakage.
func TestForbiddenStatementKeywordsAreHardErrors(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	rejected := []struct {
		name  string
		input string
	}{
		{"defer", "package main\n\nfunc f() {\n    defer Println(\"x\")\n    Println(\"y\")\n}"},
		{"go", "package main\n\nfunc f() {\n    go Println(\"x\")\n    Println(\"y\")\n}"},
		{"goto", "package main\n\nfunc f() {\n    goto Println(\"x\")\n    Println(\"y\")\n}"},
		{"fallthrough", "package main\n\nfunc f() {\n    fallthrough Println(\"x\")\n    Println(\"y\")\n}"},
		{"select", "package main\n\nfunc f() {\n    select Println(\"x\")\n    Println(\"y\")\n}"},
		{"chan", "package main\n\nfunc f() {\n    chan Println(\"x\")\n    Println(\"y\")\n}"},
	}
	for _, tc := range rejected {
		tc := tc
		t.Run("rejects_bare_"+tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "")
			require.Error(t, err, "expected bare %q statement to be rejected", tc.name)
			require.Contains(t, err.Error(), "GALA-E0036",
				"expected the forbidden-statement-keyword error code for %q, got: %v", tc.name, err)
		})
	}
}

// TestOrdinaryIdentifierStatementsAreLegal verifies the check does not fire for
// normal statements. It only targets a bare identifier equal to a forbidden Go
// keyword; a function call (which has a `(...)` postfix), or an identifier that
// merely *starts* with a forbidden word, must transpile cleanly.
func TestOrdinaryIdentifierStatementsAreLegal(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	cases := []struct {
		name  string
		input string
	}{
		// A call to a user function whose name starts with a forbidden word.
		{"deferred_call", "package main\n\nfunc deferred() {}\n\nfunc f() {\n    deferred()\n}"},
		// A plain call statement — not a bare identifier.
		{"plain_call", "package main\n\nfunc f() {\n    Println(\"y\")\n}"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "")
			require.NoError(t, err, "%s must be legal", tc.name)
		})
	}
}
