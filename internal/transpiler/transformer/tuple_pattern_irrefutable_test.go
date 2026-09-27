package transformer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsIrrefutableTuplePattern(t *testing.T) {
	tests := []struct {
		pattern string
		want    bool
	}{
		{"(_,_,err)", true},
		{"(a,b)", true},
		{"(_,_)", true},
		{"(n,(label,_))", true},
		{"((a,b),(_,(c,d)))", true},
		{"(q,r,\"ok\")", false},
		{"(0,_)", false},
		{"(n,(\"x\",true))", false},
		{"(Some(x),y)", false},
		{"(a,true)", false},
		{"(a,nil)", false},
		{"(x)", false},
		{"()", false},
		{"_", false},
		{"err", false},
		{"Tuple(a,b)", false},
		{"(a)(b)", false},
		{"(a,b", false},
		{"(a,b,c,d,e,f,g,h,i,j,k)", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			require.Equal(t, tt.want, isIrrefutableTuplePattern(tt.pattern))
		})
	}
}
