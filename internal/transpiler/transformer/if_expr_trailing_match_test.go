package transformer_test

import "testing"

// An if-expression followed by a match is the match's subject, not a bare
// if-expression: the match must not be dropped wherever the value is
// lowered — against a typed slot, as a call argument, or in statement
// position. tco_test.go covers the tail-recursive forms.
//
// A dropped match leaves the branch values in place of the arms. Every
// successful output also goes through the package's Go type-check oracle.
func TestIfExpressionWithTrailingMatch(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"typed val", "func run(c bool) string {\n    val s string = if (c) { 1 } else { 2 } match {\n        case 1 => \"one\"\n        case _ => \"other\"\n    }\n    s\n}\n"},
		{"call argument", "func show(s string) string = s\n\nfunc run(c bool) string = show(if (c) 1 else { 2 } match {\n    case 1 => \"one\"\n    case _ => \"other\"\n})\n"},
		{"statement", "func run(c bool) {\n    if (c) { 1 } else { 2 } match {\n        case 1 => Println(\"one\")\n        case _ => Println(\"other\")\n    }\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertTranspiled(t, "package main\n\n"+tc.src, []string{`"one"`, `"other"`}, nil)
		})
	}
}

// A self call under an if-expression nested in an else branch and followed by
// a match is not in tail position: the function is not rewritten into a loop.
func TestTailCallUnderNestedIfWithTrailingMatchIsNotOptimized(t *testing.T) {
	src := "package main\n\nfunc f(n int) int = if (n > 5) 1 else if (n > 0) f(n - 1) else { 0 } match {\n    case 0 => 7\n    case x => x\n}\n"
	assertTranspiled(t, src, []string{"f(n - 1)"}, []string{"for {"})
}
