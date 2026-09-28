package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A lambda passed to a method of a hand-written Go type takes its parameter
// type from the method's signature, including when that type comes from a Go
// standard-library package whose Go source uses cgo (net.Conn): the lambda is
// typed with the concrete Go type.
//
// When that parameter type, or a type inside it, cannot be resolved — the Go
// package declaring the method does not type-check — the transpile fails with
// GALA-E0033. It must not type the parameter `any`, which would only surface
// later as a `go build` error naming generated code.
func TestLambdaParamTypedFromGoMethodFuncParam(t *testing.T) {
	const galaServeOn = `package lib

import "example.com/gomethod/srv"
import "net"
import "time"

struct Conn(conn net.Conn)

func wrap(c net.Conn) Conn = Conn(c)

func ServeOn(addr string, handler func(Conn)) Try[bool] {
    return Try(() => srv.New(addr)).FlatMap((s) => {
        return Try(() => s.Serve((c) => handler(wrap(c)), time.Second)).Map((_) => true)
    })
}
`
	const galaRun = `package lib

import "example.com/gomethod/srv"

func Run() Try[bool] = Try(() => srv.New().Serve((c) => Println(c))).Map((_) => true)
`
	// unresolvedGo declares Serve with handlerType, built from a package that
	// does not exist.
	unresolvedGo := func(handlerType string) string {
		return "package srv\n\nimport \"example.com/nowhere/conn\"\n\n" +
			"type Server struct{}\n\nfunc New() *Server { return &Server{} }\n\n" +
			"func (s *Server) Serve(handler " + handlerType + ") error { return nil }\n"
	}
	const unresolvedMsg = `lambda parameter "c" has no type: Serve expects a function ` +
		`here whose parameter 1 has a type that could not be resolved`

	cases := []struct {
		name    string
		goSrc   string
		galaSrc string
		want    string // generated Go must contain it; "" when the transpile must fail
	}{
		{
			name: "net.Conn parameter",
			goSrc: `package srv

import (
	"net"
	"time"
)

type Server struct{ addr string }

func New(addr string) (*Server, error) { return &Server{addr: addr}, nil }

func (s *Server) Serve(handler func(net.Conn), shutdownTimeout time.Duration) error {
	return nil
}
`,
			galaSrc: galaServeOn,
			want:    "func(c net.Conn)",
		},
		{name: "unresolved parameter type", goSrc: unresolvedGo("func(conn.Conn)"), galaSrc: galaRun},
		{name: "unresolved type behind a pointer", goSrc: unresolvedGo("func(*conn.Conn)"), galaSrc: galaRun},
		{name: "unresolved slice element type", goSrc: unresolvedGo("func([]conn.Conn)"), galaSrc: galaRun},
		{name: "unresolved map value type", goSrc: unresolvedGo("func(map[string]conn.Conn)"), galaSrc: galaRun},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goCode, err := transpileInModule(t, map[string]string{
				"go.mod":       "module example.com/gomethod\n\ngo 1.25\n",
				"gala.mod":     "module example.com/gomethod\n",
				"srv/srv.go":   tc.goSrc,
				"lib/lib.gala": tc.galaSrc,
			}, "lib/lib.gala")
			if tc.want == "" {
				require.Error(t, err, "generated Go:\n%s", goCode)
				assert.Contains(t, err.Error(), "GALA-E0033")
				assert.Contains(t, err.Error(), unresolvedMsg)
				assert.Contains(t, err.Error(), "check that the package declaring Serve type-checks")
				return
			}
			require.NoError(t, err)
			checkGeneratedGo(t, goCode)
			assert.Contains(t, goCode, tc.want, "generated Go:\n%s", goCode)
			assert.NotContains(t, goCode, "func(c any)", "generated Go:\n%s", goCode)
		})
	}
}
