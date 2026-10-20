package generator

import (
	"bytes"
	"go/format"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInlineReturnedIIFEs(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string // empty: the source is left as it is
	}{
		{
			name: "if-expression body",
			src: `func f(c bool) int {
	return func() int {
		if c {
			return 1
		} else {
			return 2
		}
	}()
}`,
			want: `func f(c bool) int {
	{
		if c {
			return 1
		} else {
			return 2
		}
	}
}`,
		},
		{
			name: "generic method",
			src: `func (o Opt[T]) GetOrElse(d *T) *T {
	return func() *T {
		if o.p == nil {
			return d
		}
		return o.p
	}()
}`,
			want: `func (o Opt[T]) GetOrElse(d *T) *T {
	{
		if o.p == nil {
			return d
		}
		return o.p
	}
}`,
		},
		{
			name: "nested returns are inlined too",
			src: `func f(a, b bool) int {
	return func() int {
		if a {
			return func() int {
				if b {
					return 1
				}
				return 2
			}()
		}
		return 3
	}()
}`,
			want: `func f(a, b bool) int {
	{
		if a {
			{
				if b {
					return 1
				}
				return 2
			}
		}
		return 3
	}
}`,
		},
		{
			name: "return inside a loop and a function literal",
			src: `func f(xs []int) func() int {
	for range xs {
		return func() int {
			return func() int { return 1 }()
		}
	}
	return nil
}`,
			want: `func f(xs []int) func() int {
	for range xs {
		return func() int {
			{
				return 1
			}
		}
	}
	return nil
}`,
		},
		{
			name: "a defer of a function literal inside the body does not count",
			src: `func f() int {
	return func() int {
		g := func() { defer h() }
		g()
		return 1
	}()
}`,
			want: `func f() int {
	{
		g := func() { defer h() }
		g()
		return 1
	}
}`,
		},
		{
			name: "different result type",
			src: `func f() any {
	return func() int { return 1 }()
}`,
		},
		{
			name: "defer runs when the literal returns",
			src: `func f() int {
	return func() int {
		defer h()
		return 1
	}()
}`,
		},
		{
			name: "recover would start stopping a panic",
			src: `func f() any {
	return func() any {
		return recover()
	}()
}`,
		},
		{
			name: "label could clash",
			src: `func f() int {
	return func() int {
	L:
		for {
			break L
		}
		return 1
	}()
}`,
		},
		{
			name: "literal with parameters",
			src: `func f(x int) int {
	return func(y int) int { return y }(x)
}`,
		},
		{
			name: "named result",
			src: `func f() (r int) {
	return func() int { return 1 }()
}`,
		},
		{
			name: "several results",
			src: `func f() (int, error) {
	return func() (int, error) { return 1, nil }()
}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "package p\n\n" + tt.src + "\n"
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "p.go", src, 0)
			require.NoError(t, err)
			inlineReturnedIIFEs(file)
			var buf bytes.Buffer
			require.NoError(t, format.Node(&buf, fset, file))
			want := tt.want
			if want == "" {
				want = tt.src
			}
			canonical, err := format.Source([]byte("package p\n\n" + want + "\n"))
			require.NoError(t, err)
			require.Equal(t, string(canonical), buf.String())
		})
	}
}
