package transformer

// Decoding a struct with private fields.
//
// A struct with an unexported field is usually an encapsulated value: its
// package hands out instances only through a constructor that checks them
// (`func ParseEmail(s string) Option[Email]`). The StructMeta its package
// emits can build that struct from any input, so decoding it would hand an
// importer a value its constructor never accepted. Such a struct is decodable
// only when it declares
//
//	func (x T) Validate() Try[T]
//
// and its DecodeFields then returns the raw value through Validate: a
// Failure is the decoding codec's Failure, carrying the author's error. A
// struct with private fields and no such method is not decodable, and a codec
// that would decode it is GALA-E0050 at its use site; a Validate with another
// signature is GALA-E0057. A codec value both encodes and decodes, so the
// check applies to every codec that reaches the struct; encoding one means
// declaring Validate (which may accept every value) or encoding a struct of
// exported fields built from it.

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// decodeMode is how DecodeFields builds a struct.
type decodeMode uint8

const (
	// decodeRaw: every field is exported, so the decoded value is the struct.
	decodeRaw decodeMode = iota
	// decodeValidated: a private field and a Validate() Try[T]; the decoded
	// value is raw.Validate().Get().
	decodeValidated
	// decodeRejected: a private field and no usable Validate; not decodable.
	decodeRejected
)

// validateMethod is the method a struct with private fields declares to be
// decodable.
const validateMethod = "Validate"

// structDecodeMode classifies the struct meta describes (see decodeMode).
func structDecodeMode(meta *transpiler.TypeMetadata) decodeMode {
	switch {
	case !slices.ContainsFunc(meta.FieldNames, func(name string) bool { return !token.IsExported(name) }):
		return decodeRaw
	case isValidateMethod(meta, meta.Methods[validateMethod]):
		return decodeValidated
	}
	return decodeRejected
}

// isValidateMethod reports whether m is `func (x T) Validate() Try[T]` for
// meta's struct T: a value receiver, no parameters or type parameters, and a
// Try of the struct itself.
func isValidateMethod(meta *transpiler.TypeMetadata, m *transpiler.MethodMetadata) bool {
	if m == nil || m.PointerReceiver || len(m.ParamTypes) != 0 || len(m.TypeParams) != 0 {
		return false
	}
	ret, ok := m.ReturnType.(transpiler.GenericType)
	if !ok || len(ret.Params) != 1 {
		return false
	}
	if name, pkg, ok := simpleTypeName(ret.Base); !ok || name != "Try" || (pkg != "" && pkg != registry.StdPackageName) {
		return false
	}
	name, pkg, ok := simpleTypeName(ret.Params[0])
	return ok && name == meta.Name && (pkg == "" || pkg == meta.Package)
}

// undecodableError reports, at the codec use site config inherited, that
// config's struct has private fields and no usable Validate method.
func (t *galaASTTransformer) undecodableError(config *structMetaConfig) error {
	meta := config.typeMetadata
	want := validateSignature(meta)
	if declared := meta.Methods[validateMethod]; declared != nil {
		return galaerr.NewCodedSemanticError(galaerr.CodeInvalidValidateSignature, config.line, config.col,
			fmt.Sprintf("cannot generate a codec for %s: %s has private fields, and its Validate method is `%s`, not `%s`",
				config.rootName, config.typeName, declaredSignature(meta, declared), want),
			fmt.Sprintf("Validate needs a value receiver and a Try[%s] result; declare it as `%s`: "+
				"decoding builds the raw value and returns what Validate returns, so a Failure rejects the input with its error",
				meta.Name, want))
	}
	return galaerr.NewCodedSemanticError(galaerr.CodeUnsupportedCodecField, config.line, config.col,
		fmt.Sprintf("cannot generate a codec for %s: %s has private fields and no Validate method, "+
			"so decoding it would bypass its constructor", config.rootName, config.typeName),
		fmt.Sprintf("make it decodable with a Validate method; declare `%s`", want))
}

// validateSignature is the Validate method meta's struct needs, as GALA
// spells it.
func validateSignature(meta *transpiler.TypeMetadata) string {
	recv := strings.ToLower(meta.Name[:1])
	return fmt.Sprintf("func (%s %s) Validate() Try[%s]", recv, meta.Name, meta.Name)
}

// declaredSignature renders the Validate method m as declared on meta's
// struct.
func declaredSignature(meta *transpiler.TypeMetadata, m *transpiler.MethodMetadata) string {
	var sb strings.Builder
	sb.WriteString("func (")
	if m.ReceiverName != "" {
		sb.WriteString(m.ReceiverName + " ")
	}
	if m.PointerReceiver {
		sb.WriteByte('*')
	}
	sb.WriteString(meta.Name + ") Validate")
	if len(m.TypeParams) > 0 {
		sb.WriteString("[" + strings.Join(m.TypeParams, ", ") + "]")
	}
	params := make([]string, len(m.ParamTypes))
	for i, p := range m.ParamTypes {
		params[i] = galaTypeSpelling(meta, p)
		if i < len(m.ParamNames) && m.ParamNames[i] != "" {
			params[i] = m.ParamNames[i] + " " + params[i]
		}
	}
	sb.WriteString("(" + strings.Join(params, ", ") + ")")
	if m.ReturnType != nil && !m.ReturnType.IsNil() && !m.ReturnType.IsVoid() {
		sb.WriteString(" " + galaTypeSpelling(meta, m.ReturnType))
	}
	return sb.String()
}

// galaTypeSpelling renders ty as the package declaring meta's struct spells
// it: std types and that package's own types unqualified.
func galaTypeSpelling(meta *transpiler.TypeMetadata, ty transpiler.Type) string {
	s := displayType(ty)
	if meta.Package == "" {
		return s
	}
	return strings.ReplaceAll(s, meta.Package+".", "")
}

// decodedValue returns the statements that end DecodeFields or Empty for
// config's struct, given the raw value built from its fields: the value
// itself, the value through Validate, or — for a struct that is not
// decodable — a panic ahead of the body, which only Go code calling the
// metadata directly can reach, since every codec that would is rejected.
// The body stays behind the panic: dropping it would leave unused the
// imports its field reads already registered.
func (t *galaASTTransformer) decodedValue(config *structMetaConfig, body []ast.Stmt, raw ast.Expr) []ast.Stmt {
	switch structDecodeMode(config.typeMetadata) {
	case decodeValidated:
		raw = &ast.CallExpr{Fun: &ast.SelectorExpr{
			X:   &ast.CallExpr{Fun: &ast.SelectorExpr{X: raw, Sel: ast.NewIdent(validateMethod)}},
			Sel: ast.NewIdent("Get"),
		}}
	case decodeRejected:
		msg := fmt.Sprintf("%s has private fields and no `%s` method, so it cannot be decoded",
			config.typeName, validateSignature(config.typeMetadata))
		body = append([]ast.Stmt{exprStmt(&ast.CallExpr{Fun: ast.NewIdent("panic"), Args: []ast.Expr{stringLit(msg)}})}, body...)
	}
	return append(body, &ast.ReturnStmt{Results: []ast.Expr{raw}})
}
