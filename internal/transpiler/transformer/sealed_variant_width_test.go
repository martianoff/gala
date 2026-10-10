package transformer_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestSealedVariantWidth pins the field limit of a sealed variant: its
// extractor returns the fields as one std tuple, so a variant has at most as
// many fields as the widest tuple (Tuple10). A wider one is GALA-E0072 at the
// declaration rather than unparseable generated Go.
func TestSealedVariantWidth(t *testing.T) {
	cases := []struct {
		fields  int
		wantErr bool
	}{
		{fields: 10},
		{fields: 11, wantErr: true},
		{fields: 16, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d fields", tc.fields), func(t *testing.T) {
			decls := make([]string, tc.fields)
			args := make([]string, tc.fields)
			for i := range decls {
				decls[i] = fmt.Sprintf("F%d int", i)
				args[i] = fmt.Sprint(i)
			}
			src := `package main

sealed type V {
    case W(` + strings.Join(decls, ", ") + `)
    case Z()
}

func main() {
    Println(W(` + strings.Join(args, ", ") + `))
}`
			_, err := transpileBareVariant(t, src)
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodeSealedVariantTooWide))
			require.Contains(t, err.Error(), fmt.Sprintf(`sealed variant "W" has %d fields; a variant can have at most 10`, tc.fields))
		})
	}
}
