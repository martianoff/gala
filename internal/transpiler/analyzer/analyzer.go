package analyzer

import (
	"fmt"
	"io/ioutil"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"path/filepath"

	"github.com/antlr4-go/antlr/v4"
)

// GetBaseMetadata loads standard library metadata from .gala files.
func GetBaseMetadata(p transpiler.GalaParser, searchPaths []string) *transpiler.RichAST {
	base := &transpiler.RichAST{
		Types:     make(map[string]*transpiler.TypeMetadata),
		Functions: make(map[string]*transpiler.FunctionMetadata),
	}
	temp := NewGalaAnalyzer()
	stdFiles := []string{"std/option.gala", "std/immutable.gala", "std/tuple.gala", "std/either.gala"}

	for _, sf := range stdFiles {
		var content []byte
		var err error
		for _, path := range searchPaths {
			content, err = ioutil.ReadFile(filepath.Join(path, sf))
			if err == nil {
				break
			}
		}
		if err != nil {
			continue
		}
		tree, err := p.Parse(string(content))
		if err != nil {
			continue
		}
		rich, err := temp.Analyze(tree)
		if err != nil {
			continue
		}
		base.Merge(rich)
	}
	return base
}

type galaAnalyzer struct {
	baseMetadata *transpiler.RichAST
}

// NewGalaAnalyzer creates a new transpiler.Analyzer implementation.
func NewGalaAnalyzer() transpiler.Analyzer {
	return &galaAnalyzer{}
}

// NewGalaAnalyzerWithBase creates a new transpiler.Analyzer with base metadata.
func NewGalaAnalyzerWithBase(base *transpiler.RichAST) transpiler.Analyzer {
	return &galaAnalyzer{baseMetadata: base}
}

// Analyze walk the ANTLR tree and collects metadata for RichAST.
func (a *galaAnalyzer) Analyze(tree antlr.Tree) (*transpiler.RichAST, error) {
	sourceFile, ok := tree.(*grammar.SourceFileContext)
	if !ok {
		return nil, fmt.Errorf("expected *grammar.SourceFileContext, got %T", tree)
	}

	richAST := &transpiler.RichAST{
		Tree:      tree,
		Types:     make(map[string]*transpiler.TypeMetadata),
		Functions: make(map[string]*transpiler.FunctionMetadata),
	}

	// 0. Populate base metadata if provided
	if a.baseMetadata != nil {
		richAST.Merge(a.baseMetadata)
	}

	// 1. Collect all types
	for _, topDecl := range sourceFile.AllTopLevelDeclaration() {
		if typeDecl := topDecl.TypeDeclaration(); typeDecl != nil {
			ctx := typeDecl.(*grammar.TypeDeclarationContext)
			typeName := ctx.Identifier().GetText()

			var meta *transpiler.TypeMetadata
			if existing, ok := richAST.Types[typeName]; ok {
				meta = existing
			} else {
				meta = &transpiler.TypeMetadata{
					Name:    typeName,
					Methods: make(map[string]*transpiler.MethodMetadata),
					Fields:  make(map[string]string),
				}
				richAST.Types[typeName] = meta
			}

			if ctx.TypeParameters() != nil {
				tpCtx := ctx.TypeParameters().(*grammar.TypeParametersContext)
				if tpList := tpCtx.TypeParameterList(); tpList != nil {
					for _, tp := range tpList.(*grammar.TypeParameterListContext).AllTypeParameter() {
						tpId := tp.(*grammar.TypeParameterContext).Identifier(0)
						meta.TypeParams = append(meta.TypeParams, tpId.GetText())
					}
				}
			}

			if ctx.StructType() != nil {
				structType := ctx.StructType().(*grammar.StructTypeContext)
				for _, field := range structType.AllStructField() {
					fctx := field.(*grammar.StructFieldContext)
					fieldName := fctx.Identifier().GetText()
					fieldType := fctx.Type_().GetText()
					meta.Fields[fieldName] = fieldType
					meta.FieldNames = append(meta.FieldNames, fieldName)
					meta.ImmutFlags = append(meta.ImmutFlags, fctx.VAR() == nil)
				}
			}
		}

		if shorthandCtx := topDecl.StructShorthandDeclaration(); shorthandCtx != nil {
			ctx := shorthandCtx.(*grammar.StructShorthandDeclarationContext)
			typeName := ctx.Identifier().GetText()

			var meta *transpiler.TypeMetadata
			if existing, ok := richAST.Types[typeName]; ok {
				meta = existing
			} else {
				meta = &transpiler.TypeMetadata{
					Name:    typeName,
					Methods: make(map[string]*transpiler.MethodMetadata),
					Fields:  make(map[string]string),
				}
				richAST.Types[typeName] = meta
			}

			if ctx.Parameters() != nil {
				paramsCtx := ctx.Parameters().(*grammar.ParametersContext)
				if paramsCtx.ParameterList() != nil {
					for _, param := range paramsCtx.ParameterList().(*grammar.ParameterListContext).AllParameter() {
						pctx := param.(*grammar.ParameterContext)
						fieldName := pctx.Identifier().GetText()
						fieldType := ""
						if pctx.Type_() != nil {
							fieldType = pctx.Type_().GetText()
						}
						meta.Fields[fieldName] = fieldType
						meta.FieldNames = append(meta.FieldNames, fieldName)
						meta.ImmutFlags = append(meta.ImmutFlags, pctx.VAR() == nil)
					}
				}
			}
		}
	}

	// 2. Collect methods and functions
	for _, topDecl := range sourceFile.AllTopLevelDeclaration() {
		if funcDeclCtx := topDecl.FunctionDeclaration(); funcDeclCtx != nil {
			ctx := funcDeclCtx.(*grammar.FunctionDeclarationContext)
			if ctx.Receiver() != nil {
				recvCtx := ctx.Receiver().(*grammar.ReceiverContext)
				baseType := getBaseTypeName(recvCtx.Type_())
				if baseType != "" {
					methodName := ctx.Identifier().GetText()
					methodMeta := &transpiler.MethodMetadata{
						Name: methodName,
					}
					if ctx.TypeParameters() != nil {
						tpCtx := ctx.TypeParameters().(*grammar.TypeParametersContext)
						if tpList := tpCtx.TypeParameterList(); tpList != nil {
							for _, tp := range tpList.(*grammar.TypeParameterListContext).AllTypeParameter() {
								tpId := tp.(*grammar.TypeParameterContext).Identifier(0)
								methodMeta.TypeParams = append(methodMeta.TypeParams, tpId.GetText())
							}
						}
					}

					if ctx.Signature().Type_() != nil {
						methodMeta.ReturnType = getBaseTypeName(ctx.Signature().Type_())
					}

					if typeMeta, ok := richAST.Types[baseType]; ok {
						if existing, exists := typeMeta.Methods[methodName]; exists {
							// Preserve IsGeneric if it was pre-populated
							methodMeta.IsGeneric = existing.IsGeneric
						}
						typeMeta.Methods[methodName] = methodMeta
					} else {
						// Even if type is not in this file, we might want to collect it?
						// But for now let's stick to what's requested.
						// We can create a placeholder if needed.
						richAST.Types[baseType] = &transpiler.TypeMetadata{
							Name:    baseType,
							Methods: map[string]*transpiler.MethodMetadata{methodName: methodMeta},
							Fields:  make(map[string]string),
						}
					}
				}
			} else {
				// Top-level function
				funcName := ctx.Identifier().GetText()
				funcMeta := &transpiler.FunctionMetadata{
					Name: funcName,
				}
				if ctx.Signature().Type_() != nil {
					funcMeta.ReturnType = getBaseTypeName(ctx.Signature().Type_())
				}
				if ctx.TypeParameters() != nil {
					tpCtx := ctx.TypeParameters().(*grammar.TypeParametersContext)
					if tpList := tpCtx.TypeParameterList(); tpList != nil {
						for _, tp := range tpList.(*grammar.TypeParameterListContext).AllTypeParameter() {
							tpId := tp.(*grammar.TypeParameterContext).Identifier(0)
							funcMeta.TypeParams = append(funcMeta.TypeParams, tpId.GetText())
						}
					}
				}
				richAST.Functions[funcName] = funcMeta
			}
		}
	}

	return richAST, nil
}

func getBaseTypeName(ctx grammar.ITypeContext) string {
	if ctx == nil {
		return ""
	}
	if ctx.Identifier() != nil {
		return ctx.Identifier().GetText()
	}
	if len(ctx.AllType_()) > 0 {
		// Handles pointers (*T) and potentially other nested types
		return getBaseTypeName(ctx.Type_(0))
	}
	return ""
}

var _ transpiler.Analyzer = (*galaAnalyzer)(nil)
