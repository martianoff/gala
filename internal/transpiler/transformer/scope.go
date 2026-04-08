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
	// Collect for LSP — scope by current function to prevent cross-function collisions
	if t.lspVarTypes != nil && typeName != nil && !typeName.IsNil() {
		key := name
		if t.lspCurrentFunc != "" {
			key = t.lspCurrentFunc + "." + name
		}
		t.lspVarTypes[key] = typeName
	}
}

func (t *galaASTTransformer) addVar(name string, typeName transpiler.Type) {
	if t.currentScope != nil {
		t.currentScope.vals[name] = false
		t.currentScope.valTypes[name] = typeName
	}
	// Collect for LSP — scope by current function to prevent cross-function collisions
	if t.lspVarTypes != nil && typeName != nil && !typeName.IsNil() {
		key := name
		if t.lspCurrentFunc != "" {
			key = t.lspCurrentFunc + "." + name
		}
		t.lspVarTypes[key] = typeName
	}
}

// collectAllVarTypes returns all variable types from the current scope stack.
// Used by LSP to expose the transformer's resolved types.
func (t *galaASTTransformer) collectAllVarTypes() map[string]transpiler.Type {
	result := make(map[string]transpiler.Type)
	s := t.currentScope
	for s != nil {
		for name, typ := range s.valTypes {
			if _, exists := result[name]; !exists {
				result[name] = typ
			}
		}
		s = s.parent
	}
	return result
}

func (t *galaASTTransformer) getType(name string) transpiler.Type {
	// 1. If name already has a dot, it might be pkg.Type - resolve alias and check directly
	if strings.Contains(name, ".") {
		resolvedName := name
		parts := strings.Split(name, ".")
		if actual, ok := t.importManager.ResolveAlias(parts[0]); ok {
			resolvedName = actual + "." + parts[1]
		}
		if _, ok := t.typeMetas[resolvedName]; ok {
			result := transpiler.ParseType(resolvedName)
			t.traceType(nil, result, "scope:qualified:"+name)
			return result
		}
		// If it has a dot but not found in metas, don't fall through to other searches
		t.traceType(nil, transpiler.NilType{}, "scope:qualified-miss:"+name)
		return transpiler.NilType{}
	}

	// 2. Search in current scope (local variables have highest priority)
	s := t.currentScope
	for s != nil {
		if typeName, ok := s.valTypes[name]; ok {
			t.traceType(nil, typeName, "scope:local:"+name)
			return typeName
		}
		s = s.parent
	}

	// 3. Use unified type resolution for type metadata lookup
	resolved := t.resolveTypeMetaName(name)
	if resolved != "" {
		result := transpiler.ParseType(resolved)
		t.traceType(nil, result, "scope:type-meta:"+name)
		return result
	}

	t.traceType(nil, transpiler.NilType{}, "scope:miss:"+name)
	return transpiler.NilType{}
}

func (t *galaASTTransformer) getValType(name string) transpiler.Type {
	s := t.currentScope
	for s != nil {
		if typeName, ok := s.valTypes[name]; ok {
			return typeName
		}
		s = s.parent
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
	// Use unified resolution to find the function
	resolved, found := t.resolveTypeName(name, func(n string) bool {
		_, ok := t.functions[n]
		return ok
	})
	if found {
		return t.functions[resolved]
	}
	return nil
}
