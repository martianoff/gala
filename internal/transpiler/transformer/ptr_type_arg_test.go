package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ptrTypeArgDecls declares, beside ptrReceiverDecls, generic functions, a
// generic method and a generic type whose type parameter is constrained by an
// interface with a method Counter declares on a Ptr receiver (Bumper), and
// ones constrained by `any` and by an interface of a `*T` method (Peeker).
const ptrTypeArgDecls = `type Bumper interface {
    Bump()
}

type Peeker interface {
    Peek() int
}

func (c *Counter) Peek() int = if (c == nil) 0 else c.N

func bumpIt[B Bumper](b B) = b.Bump()

func bumpAll[B Bumper](bs Array[B]) = bs.ForEach((b) => b.Bump())

func peekIt[P Peeker](p P) int = p.Peek()

func same[T any](x T) T = x

struct Runner(N int)

func (r Runner) Run[B Bumper](b B) = b.Bump()

struct Holder[B Bumper](b B)

func (h Holder[B]) Fire() = h.b.Bump()
`

// ptrTypeArgSource is a program of ptrReceiverDecls, ptrTypeArgDecls and
// decls, whose main runs body.
func ptrTypeArgSource(decls, body string) string {
	return "package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\n" +
		ptrReceiverDecls + "\n" + ptrTypeArgDecls + "\n" + decls + "\nfunc main() {\n    " + body + "\n}\n"
}

// TestPtrTypeArgRejected checks that a pointer that may be nil is never the
// type argument of a type parameter whose constraint has a method its type
// declares on a Ptr receiver (GALA-E0080), written or inferred, of a generic
// function, method or type (#802): the generic body calls the method on
// whatever pointer the type argument holds.
func TestPtrTypeArgRejected(t *testing.T) {
	trans := newTranspiler()
	tests := []struct {
		name  string
		decls string
		body  string
		msg   string
		hint  string
	}{
		{
			name: "inferred from an argument",
			body: "bumpIt(lookup(false))",
			msg:  "type argument B (Bumper) of bumpIt is *Counter, inferred from lookup(false): its method Bump has a Ptr[Counter] receiver, and a pointer that may be nil cannot fill a Ptr",
			hint: "pass RequirePtr(lookup(false)); RequirePtr panics on nil",
		},
		{
			name: "inferred from a nullable val",
			body: "val c = lookup(true)\n    bumpIt(c)",
			msg:  "type argument B (Bumper) of bumpIt is *Counter, inferred from c",
		},
		{
			name: "inferred from inside an argument",
			body: "bumpAll(ArrayOf(lookup(true)))",
			msg:  "type argument B (Bumper) of bumpAll is *Counter, inferred from ArrayOf(lookup(true))",
			hint: "make it a Ptr[Counter]: `&x`, or RequirePtr(p) for a pointer that may be nil",
		},
		{
			name: "written",
			body: "bumpIt[*Counter](lookup(true))",
			msg:  "type argument B (Bumper) of bumpIt is *Counter: its method Bump has a Ptr[Counter] receiver",
			hint: "make the type argument Ptr[Counter]",
		},
		{
			name: "written, of a function value",
			body: "val f = bumpIt[*Counter]\n    f(RequirePtr(lookup(true)))",
			msg:  "type argument B (Bumper) of bumpIt is *Counter",
		},
		{
			name: "inferred, of a generic method",
			body: "Runner(0).Run(lookup(true))",
			msg:  "type argument B (Bumper) of Run is *Counter, inferred from lookup(true)",
		},
		{
			name: "written, of a generic method",
			body: "Runner(0).Run[*Counter](lookup(true))",
			msg:  "type argument B (Bumper) of Run is *Counter",
		},
		{
			name: "inferred, of a generic type",
			body: "Holder(lookup(true)).Fire()",
			msg:  "type argument B (Bumper) of Holder is *Counter, inferred from lookup(true)",
		},
		{
			name: "written, of a generic type",
			body: "Holder[*Counter](lookup(true)).Fire()",
			msg:  "type argument B (Bumper) of Holder is *Counter",
		},
		{
			name: "inferred, of a generic method, from an argument holding a lambda",
			body: "Runner(0).Run(Some(lookup(true)).Map((c) => c).GetOrElse(lookup(false)))",
			msg:  "type argument B (Bumper) of Run is *Counter, inferred from Some(lookup(true))",
		},
		{
			name:  "inferred from the expected type",
			decls: "func mk[B Bumper]() Array[B] = EmptyArray[B]()\n",
			body:  "val xs Array[*Counter] = mk()\n    Println(xs.Size())",
			msg:   "type argument B (Bumper) of mk is *Counter, inferred from the type the call is expected to have",
		},
		{
			name:  "function reference in a function slot",
			decls: "func each(f func(*Counter)) = f(lookup(false))\n",
			body:  "each(bumpIt)",
			msg:   "type argument B (Bumper) of bumpIt is *Counter",
		},
		{
			name:  "inferred from a lambda",
			decls: "func run[B Bumper](f func() B) = f().Bump()\n",
			body:  "run(() => lookup(false))",
			msg:   "type argument B (Bumper) of run is *Counter, inferred from () => lookup(false)",
		},
		{
			name:  "inferred from a variadic generic method's later argument",
			decls: "func (r Runner) RunAll[B Bumper](bs ...B) = Println(r.N)\n",
			body:  "var c = Counter(0)\n    Runner(0).RunAll(&c, lookup(false))",
			msg:   "type argument B (Bumper) of RunAll is *Counter, inferred from lookup(false)",
		},
		{
			name:  "function reference typed by the call's own type arguments",
			decls: "func apply[T any](f func(T), x T) = f(x)\n",
			body:  "apply(bumpIt, lookup(false))",
			msg:   "type argument B (Bumper) of bumpIt is *Counter",
		},
		{
			name:  "function reference typed by the call's written type arguments",
			decls: "func applyTo[T any](f func(T), x T) = f(x)\n",
			body:  "applyTo[*Counter](bumpIt, lookup(false))",
			msg:   "type argument B (Bumper) of bumpIt is *Counter",
		},
		{
			name:  "function reference typed by a generic method's later argument",
			decls: "func (r Runner) Each[T any](f func(T), x T) = f(x)\n",
			body:  "Runner(0).Each(bumpIt, lookup(false))",
			msg:   "type argument B (Bumper) of bumpIt is *Counter",
		},
		{
			name:  "inferred, of a generic method, from a lambda",
			decls: "func (r Runner) RunWith[B Bumper](f func() B) = f().Bump()\n",
			body:  "Runner(0).RunWith(() => lookup(false))",
			msg:   "type argument B (Bumper) of RunWith is *Counter, inferred from () => lookup(false)",
		},
		{
			name:  "function reference in a slot that also takes a Ptr",
			decls: "func both[A Bumper, B Bumper](a A, b B) {\n    a.Bump()\n    b.Bump()\n}\n\nfunc each2(f func(Ptr[Counter], *Counter)) {\n    var c = Counter(0)\n    f(&c, lookup(false))\n}\n",
			body:  "each2(both)",
			msg:   "type argument B (Bumper) of both is *Counter",
		},
		{
			name:  "inferred, of a generic sealed type's variant",
			decls: "sealed type Job[B Bumper] {\n    case Begin(b B)\n}\n",
			body:  "val j = Begin(lookup(true))\n    Println(j)",
			msg:   "type argument B (Bumper) of Begin is *Counter, inferred from lookup(true)",
		},
		{
			name: "function reference as a generic method's argument",
			body: "ArrayOf(lookup(true)).ForEach(bumpIt)",
			msg:  "type argument B (Bumper) of bumpIt is *Counter",
		},
		{
			name:  "written, of a generic sealed type",
			decls: "sealed type Job[B Bumper] {\n    case Begin(b B)\n}\n\nfunc start(j Job[*Counter]) = Println(\"start\")\n",
			body:  "var c = Counter(0)\n    start(Begin(&c))",
			msg:   "type argument B (Bumper) of Job is *Counter",
		},
		{
			name:  "written, of a generic type in a declared type",
			decls: "func fire(h Holder[*Counter]) = h.Fire()\n",
			body:  "var c = Counter(0)\n    fire(Holder(&c))",
			msg:   "type argument B (Bumper) of Holder is *Counter",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(ptrTypeArgSource(tt.decls, tt.body), "")
			require.Error(t, err)
			var semErr *galaerr.SemanticError
			require.ErrorAs(t, err, &semErr)
			assert.Equal(t, galaerr.CodePtrNullability, semErr.Code, "%v", err)
			assert.Contains(t, semErr.Msg, tt.msg)
			assert.Contains(t, semErr.Hint, tt.hint)
		})
	}
}

// TestPtrTypeArgAllowed checks the type arguments that are never nil — a Ptr,
// written or inferred from `&x` or RequirePtr — and the type parameters whose
// constraint needs no Ptr receiver: `any`, and an interface of `*T` methods.
// A type parameter passed on as the type argument of another is checked
// where its own type argument is given.
func TestPtrTypeArgAllowed(t *testing.T) {
	trans := newTranspiler()
	tests := []struct {
		name  string
		decls string
		body  string
	}{
		{name: "address", body: "var c = Counter(0)\n    bumpIt(&c)\n    Println(c.N)"},
		{name: "RequirePtr", body: "bumpIt(RequirePtr(lookup(true)))"},
		{name: "written Ptr", body: "var c = Counter(0)\n    bumpIt[Ptr[Counter]](&c)"},
		{name: "array of Ptr", body: "var c = Counter(0)\n    bumpAll(ArrayOf(&c))"},
		{name: "any constraint", body: "Println(same(lookup(false)) == nil)"},
		{name: "constraint of *T methods", body: "Println(peekIt(lookup(false)))"},
		{name: "generic method", body: "var c = Counter(0)\n    Runner(0).Run(&c)"},
		{
			name:  "Ptr of a generic sealed type",
			decls: "sealed type Job[B Bumper] {\n    case Begin(b B)\n}\n\nfunc start(j Job[Ptr[Counter]]) = Println(\"start\")\n",
			body:  "var c = Counter(0)\n    start(Begin(&c))",
		},
		{
			name:  "function reference taking a Ptr",
			decls: "func each(f func(Ptr[Counter])) {\n    var c = Counter(0)\n    f(&c)\n}\n",
			body:  "each(bumpIt)",
		},
		{name: "generic type", body: "var c = Counter(0)\n    Holder(&c).Fire()"},
		{name: "written Ptr of a generic type", decls: "func fire(h Holder[Ptr[Counter]]) = h.Fire()\n", body: "var c = Counter(0)\n    fire(Holder(&c))"},
		{
			name:  "type parameter passed on",
			decls: "func relay[B Bumper](b B) = bumpIt(b)\n\nfunc relayAll[B Bumper](b B) = bumpAll(ArrayOf(b))\n",
			body:  "var c = Counter(0)\n    relay(&c)\n    relayAll(&c)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(ptrTypeArgSource(tt.decls, tt.body), "")
			require.NoError(t, err)
		})
	}
}

// TestPtrTypeArgCrossPackage checks that the constraint of a type parameter
// declared in another package is enforced at a call site: it is part of the
// function's and method's metadata.
func TestPtrTypeArgCrossPackage(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/ptrtypearg\n\ngala dev\n")
	write("api/api.gala", "package api\n\ntype Kicker interface {\n    Bump()\n}\n")
	write("lib/lib.gala", `package lib

import . "example.com/ptrtypearg/api"

struct Counter(var N int)

func (c Ptr[Counter]) Bump() {
    c.N = c.N + 1
}

type Bumper interface {
    Bump()
}

func BumpIt[B Bumper](b B) = b.Bump()

func KickIt[K Kicker](k K) = k.Bump()

struct Runner(N int)

func (r Runner) Run[B Bumper](b B) = b.Bump()

func Lookup(found bool) *Counter = if (found) &Counter(1) else nil
`)
	tests := []struct {
		name, body, msg string
	}{
		{name: "inferred", body: "lib.BumpIt(lib.Lookup(true))", msg: "type argument B (lib.Bumper) of lib.BumpIt is *lib.Counter, inferred from lib.Lookup(true)"},
		{name: "written", body: "lib.BumpIt[*lib.Counter](lib.Lookup(true))", msg: "type argument B (lib.Bumper) of lib.BumpIt is *lib.Counter"},
		{name: "generic method", body: "lib.Runner(0).Run(lib.Lookup(true))", msg: "type argument B (lib.Bumper) of Run is *lib.Counter"},
		{name: "constraint from a dot-import", body: "lib.KickIt(lib.Lookup(true))", msg: "type argument K (api.Kicker) of lib.KickIt is *lib.Counter"},
		{name: "Ptr", body: "var c = lib.Counter(0)\n    lib.BumpIt(&c)\n    lib.Runner(0).Run(&c)\n    Println(c.N)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := transpileCrossPkg(t, root, "package main\n\nimport \"example.com/ptrtypearg/lib\"\n\nfunc main() {\n    "+tt.body+"\n}\n")
			if tt.msg == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			var semErr *galaerr.SemanticError
			require.ErrorAs(t, err, &semErr)
			assert.Equal(t, galaerr.CodePtrNullability, semErr.Code, "%v", err)
			assert.Contains(t, semErr.Msg, tt.msg)
		})
	}
}
