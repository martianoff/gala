package gala2lean

import (
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/formal/coreexport"
)

// TestConstValue checks which constant expressions checkUntypedConstant
// reports: those whose Int64 translation may differ from Go's exact value.
func TestConstValue(t *testing.T) {
	lit := func(text string) coreexport.Node { return &coreexport.Int{Text: text} }
	bin := func(op string, l, r coreexport.Node) coreexport.Node {
		return &coreexport.Binary{Op: op, Left: l, Right: r}
	}
	neg := func(x coreexport.Node) coreexport.Node { return &coreexport.Unary{Op: "-", Operand: x} }
	name := &coreexport.Name{Name: "x"}
	tests := []struct {
		name string
		expr coreexport.Node
		want constState
	}{
		{"#1237: (1 << 70) >> 68", bin(">>", bin("<<", lit("1"), lit("70")), lit("68")), mayDiffer},
		{"#1237: max * 2 / 4", bin("/", bin("*", lit("9223372036854775807"), lit("2")), lit("4")), mayDiffer},
		{"in range", bin("+", bin("*", lit("6"), lit("7")), lit("0x10")), exact},
		{"min int literal", neg(lit("9223372036854775808")), exact},
		{"negating min int", neg(neg(lit("9223372036854775808"))), mayDiffer},
		{"shift by 63", bin(">>", neg(lit("1")), lit("63")), exact},
		{"shift by 64", bin(">>", lit("1"), lit("64")), mayDiffer},
		{"constant division by zero", bin("/", lit("1"), lit("0")), mayDiffer},
		{"comparison of large constants", bin(">", bin("<<", lit("1"), lit("62")), lit("0")), exact},
		{"a name is not a constant", bin("+", name, lit("1")), notConst},
		{"strings", bin("+", &coreexport.String{Value: "a"}, &coreexport.String{Value: "b"}), exact},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := constValue(tt.expr)
			require.Equal(t, tt.want, got)
		})
	}
}
