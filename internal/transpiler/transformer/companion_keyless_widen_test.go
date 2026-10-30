package transformer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestKeylessCaseWidensToItsParent: a sealed case of a main or test package
// (whose companions have no qualified key) widens to its parent where match
// arms meet, in a tuple slot and in a value of the declared result type. The
// case type may name its package, `main.MsgCmd`, while its companion is filed
// under the bare name `MsgCmd`; the widening must still find it.
func TestKeylessCaseWidensToItsParent(t *testing.T) {
	const body = `

sealed type AppMsg {
    case Tick()
    case Failed(Reason string)
}

sealed type Cmd[T any] {
    case NoCmd()
    case MsgCmd(Msg T)
}

struct Model(Counter int)

func none() Cmd[AppMsg] = NoCmd[AppMsg]()

func step(n int) Tuple[Model, Cmd[AppMsg]] = n match {
    case 0 => {
        val cmd = MsgCmd[AppMsg](Msg = Failed(Reason = "boom"))
        (Model(Counter = 1), cmd)
    }
    case _ => (Model(Counter = 2), none())
}

func pick(n int) Cmd[AppMsg] = n match {
    case 0 => {
        val cmd = MsgCmd[AppMsg](Msg = Tick())
        cmd
    }
    case _ => none()
}

func Run() string = s"${step(0).V1.Counter} ${pick(0)}"
`
	for _, pkg := range []string{"main", "test"} {
		t.Run(pkg, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "gala.mod"), []byte("module example.com/widen\n\ngala dev\n"), 0644))
			src := "package " + pkg + body
			if pkg == "main" {
				src += "\nfunc main() { Println(Run()) }\n"
			}
			_, err := transpileCrossPkgFile(t, root, src, filepath.Join(root, strings.ToLower(pkg)+".gala"))
			require.NoError(t, err)
		})
	}
}
