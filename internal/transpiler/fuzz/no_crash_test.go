package fuzz_test

import "testing"

// crashSeeds are small programs, whole or broken, around constructs that
// have crashed or mis-transpiled before.
var crashSeeds = []string{
	"",
	"package",
	"package main",
	"package main\n",
	"package main\n\nfunc main() {}\n",
	"package main\nfunc main() {}\n",
	"package main\n\nfunc main() {\n    Println(s\"v=${1 +}\")\n}\n",
	"package main\n\nfunc main() {\n    val x = 1\n    Println(s\"v=${x\")\n}\n",
	"package main\n\nfunc main() {\n    Println(f\"${3.5}%.2f $$ ${\"}\"}\")\n}\n",
	"package main\n\nfunc main() {\n    Println(\"(\\d{4})\")\n}\n",
	"package main\n\nfunc main() {\n    Println(\"//line other.gala:1\")\n}\n",
	"package main\n\nfunc main() {\n    Println(`__gala_line_3`)\n}\n",
	"package main\n\nfunc main() {\n    val x = 1 match {\n        case 1 => \"one\"\n        case _ => \"other\"\n    }\n    Println(x)\n}\n",
	"package main\n\nsealed type Shape {\n    case Circle(R float64)\n    case Square(S float64)\n}\n\nfunc main() {\n    val s = Circle(1.0)\n    Println(s)\n}\n",
	"package main\n\nstruct P(X int, Y int)\n\nfunc main() {\n    val p = P(1, 2)\n    Println(p.Copy(X = 3))\n}\n",
	"package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() {\n    Println(ArrayOf(1, 2, 3).Map((x) => x * 2))\n}\n",
	"package main\n\nfunc f[T any](x T) T = x\n\nfunc main() {\n    Println(f(1))\n}\n",
	"package main\n\nfunc main() {\n    var i = 0\n    for i < 3 {\n        i = i + 1\n    }\n    Println(i)\n}\n",
	"\xef\xbb\xbfpackage main\n\nfunc main() {\n    Println(\"bom\")\n}\n",
	"package main\n\n// é ✓ 𝄞\nfunc main() {\n    /* 日本 */ Println(\"ü\")\n}\n",
	"package main\n\nfunc main() {\n    Println('\\x41')\n}\n",
	"package main\n\nfunc main() {\n    Println(\"\x00\")\n}\n",
	"package main\r\n\r\nfunc main() {\r\n    Println(\"crlf\")\r\n}\r\n",
	"package main\n\nfunc main() {\n    val perm = 0644\n    val bad = 08\n    Println(perm, bad)\n}\n",
	"package main\n\nimport \"martianoff/gala/nosuch/pkg\"\n\nfunc main() {\n    Println(pkg.X, Undefined1, Undefined2)\n}\n",
}

// FuzzTranspileNoCrash feeds arbitrary bytes and mutated example programs to
// the whole pipeline and asserts only the shared properties: no panic, no hang,
// no internal error, every rejection a coded diagnostic positioned inside the
// input, and every success parseable Go without leaked markers.
func FuzzTranspileNoCrash(f *testing.F) {
	for _, s := range crashSeeds {
		f.Add(s)
	}
	for _, s := range exampleSeeds(f) {
		f.Add(s)
	}
	warmUp(f)
	f.Fuzz(func(t *testing.T, src string) {
		first := transpile(t, src)
		checkOutcome(t, src, first)
		// Determinism: a second, independent transpile must produce the
		// same Go byte for byte. Ranging over a map while emitting code
		// shows up here, since Go randomises map iteration order.
		if first.Err == nil {
			if second := transpile(t, src); second.Err != nil || second.Go != first.Go {
				t.Fatalf("transpiling the same input twice gave different results (%v)\n%s\ninput:\n%q",
					second.Err, firstDiff(first.Go, second.Go), src)
			}
		}
	})
}
