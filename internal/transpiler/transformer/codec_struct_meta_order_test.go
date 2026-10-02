package transformer_test

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStructMetaDeclarationsAreOrdered pins the order of the generated
// _StructMeta_X declarations. They were emitted by ranging over a map, so a
// program with several codec'd structs produced a different file on every
// run — found by the transpiler fuzzer's determinism check. Name order is
// what the transformer emits now; comparing several independent runs guards
// the determinism itself.
func TestStructMetaDeclarationsAreOrdered(t *testing.T) {
	src := `package main

import (
    . "martianoff/gala/std"
    . "martianoff/gala/json"
)

struct Delta(W string, Inner Alpha)
struct Charlie(Z int)
struct Bravo(Y string)
struct Alpha(X int)

func main() {
    Println(Codec[Delta](SnakeCase()).Encode(Delta("d", Alpha(4))).Get())
    Println(Codec[Charlie](SnakeCase()).Encode(Charlie(3)).Get())
    Println(Codec[Bravo](SnakeCase()).Encode(Bravo("b")).Get())
}
`
	decl := regexp.MustCompile(`(?m)^type (_StructMeta_\w+) struct`)
	var first string
	for run := 0; run < 5; run++ {
		out, err := newTranspiler().Transpile(src, "codec_order.gala")
		require.NoError(t, err)
		if run == 0 {
			first = out
			var names []string
			for _, m := range decl.FindAllStringSubmatch(out, -1) {
				names = append(names, m[1])
			}
			// Alpha is reached only through Delta's field, so this also
			// covers the nested registrations.
			sfx := metaSuffix("codec_order.gala")
			require.Equal(t, []string{"_StructMeta_Alpha" + sfx, "_StructMeta_Bravo" + sfx, "_StructMeta_Charlie" + sfx, "_StructMeta_Delta" + sfx}, names)
			continue
		}
		require.Equal(t, first, out, "run %d produced different Go", run)
	}
}
