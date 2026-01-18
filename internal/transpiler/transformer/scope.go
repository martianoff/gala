package transformer

import (
	"martianoff/gala/internal/transpiler"
	"strings"
)

type scope struct {
	vals     map[string]bool
	valTypes map[string]transpiler.Type
	parent   *scope
}

func (t *galaASTTransformer) pushScope() {
	t.currentScope = &scope{
		vals:     make(map[string]bool),
		valTypes: make(map[string]transpiler.Type),
		parent:   t.currentScope,
	}
}

func (t *galaASTTransformer) popScope() {
	if t.currentScope != nil {
		t.currentScope = t.currentScope.parent
	}
}

func (t *galaASTTransformer) addVal(name string, typeName transpiler.Type) {
	if t.currentScope != nil {
		t.currentScope.vals[name] = true
		t.currentScope.valTypes[name] = typeName
	}
}

func (t *galaASTTransformer) addVar(name string, typeName transpiler.Type) {
	if t.currentScope != nil {
		t.currentScope.vals[name] = false
		t.currentScope.valTypes[name] = typeName
	}
}

func (t *galaASTTransformer) getType(name string) transpiler.Type {
	// 1. If name already has a dot, it might be pkg.Type
	if strings.Contains(name, ".") {
		resolvedName := name
		parts := strings.Split(name, ".")
		if actual, ok := t.importAliases[parts[0]]; ok {
			resolvedName = actual + "." + parts[1]
		}
		if _, ok := t.typeMetas[resolvedName]; ok {
			return transpiler.ParseType(resolvedName)
		}
		// If it has a dot but not found in metas, don't fall through to other searches
		return transpiler.NilType{}
	}

	// 2. Search in current scope
	s := t.currentScope
	for s != nil {
		if typeName, ok := s.valTypes[name]; ok {
			return typeName
		}
		s = s.parent
	}

	// 3. Search in current package symbols (no prefix in GALA, but might be prefixed in RichAST)
	if _, ok := t.typeMetas[name]; ok {
		return transpiler.ParseType(name)
	}
	if t.packageName != "" && t.packageName != "main" {
		fullName := t.packageName + "." + name
		if _, ok := t.typeMetas[fullName]; ok {
			return transpiler.ParseType(fullName)
		}
	}

	// 4. Search in dot-imported packages
	for _, pkg := range t.dotImports {
		fullName := pkg + "." + name
		if _, ok := t.typeMetas[fullName]; ok {
			return transpiler.ParseType(fullName)
		}
	}

	// 5. Search in all imported packages (including implicit std import)
	for alias := range t.imports {
		actualPkg := alias
		if actual, ok := t.importAliases[alias]; ok {
			actualPkg = actual
		}
		fullName := actualPkg + "." + name
		if _, ok := t.typeMetas[fullName]; ok {
			return transpiler.ParseType(fullName)
		}
	}

	return transpiler.NilType{}
}

func (t *galaASTTransformer) isVal(name string) bool {
	s := t.currentScope
	for s != nil {
		if isImmutable, ok := s.vals[name]; ok {
			return isImmutable
		}
		s = s.parent
	}
	return false
}

func (t *galaASTTransformer) isVar(name string) bool {
	s := t.currentScope
	for s != nil {
		if isImmutable, ok := s.vals[name]; ok {
			return !isImmutable
		}
		s = s.parent
	}
	return false
}

func (t *galaASTTransformer) getFunction(name string) *transpiler.FunctionMetadata {
	if fMeta, ok := t.functions[name]; ok {
		return fMeta
	}
	if t.packageName != "" && t.packageName != "main" {
		fullName := t.packageName + "." + name
		if fMeta, ok := t.functions[fullName]; ok {
			return fMeta
		}
	}
	for _, pkg := range t.dotImports {
		fullName := pkg + "." + name
		if fMeta, ok := t.functions[fullName]; ok {
			return fMeta
		}
	}
	// Search in all imported packages (including implicit std import)
	for alias := range t.imports {
		actualPkg := alias
		if actual, ok := t.importAliases[alias]; ok {
			actualPkg = actual
		}
		fullName := actualPkg + "." + name
		if fMeta, ok := t.functions[fullName]; ok {
			return fMeta
		}
	}
	return nil
}
