package generator

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/token"
	"martianoff/gala/internal/transpiler"
)

type goCodeGenerator struct {
}

// NewGoCodeGenerator creates a new instance of CodeGenerator that generates Go code.
func NewGoCodeGenerator() transpiler.CodeGenerator {
	return &goCodeGenerator{}
}

// Generate implements the CodeGenerator interface.
func (g *goCodeGenerator) Generate(fset *token.FileSet, file *ast.File) (string, error) {
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		return "", err
	}
	return buf.String(), nil
}

var _ transpiler.CodeGenerator = (*goCodeGenerator)(nil)
