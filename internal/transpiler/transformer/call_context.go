package transformer

import (
	"martianoff/gala/internal/transpiler"
)

// callContext bundles everything needed to resolve expected types for arguments
// in a function or method call. It is used by resolveExpectedArgType and
// resolveNamedArgExpectedType to provide a unified expected-type resolution
// across generic method calls, regular method calls, and function calls.
type callContext struct {
	methodMeta      *transpiler.MethodMetadata   // non-nil for method calls
	funcMeta        *transpiler.FunctionMetadata // non-nil for function calls
	applyMethodMeta *transpiler.MethodMetadata   // non-nil for companion-Apply calls (Type[T](args))
	applyTypeSubst  map[string]string            // type-param substitutions derived from Type[T]'s indices
	typeSubst       map[string]string            // generic type param substitutions (type param name -> concrete type string)
	goParamTypes    []transpiler.Type            // Go type info fallback param types (for Go-defined functions)
	structFields    []transpiler.Type            // struct construction fallback field types
	unresolvedTP    bool                         // if true, only pass void FuncTypes through (unresolved type params)
}

// buildMethodCallContext creates a callContext for a method call with resolved type params.
func (t *galaASTTransformer) buildMethodCallContext(
	methodMeta *transpiler.MethodMetadata,
	typeSubst map[string]string,
	unresolvedTP bool,
) callContext {
	return callContext{
		methodMeta:   methodMeta,
		typeSubst:    typeSubst,
		unresolvedTP: unresolvedTP,
	}
}

// buildFuncCallContext creates a callContext for a regular function call,
// including Go type info and struct field fallbacks.
func (t *galaASTTransformer) buildFuncCallContext(
	funcMeta *transpiler.FunctionMetadata,
	inferredTypeSubst map[string]string,
	goParamTypes []transpiler.Type,
	structFields []transpiler.Type,
) callContext {
	return callContext{
		funcMeta:     funcMeta,
		typeSubst:    inferredTypeSubst,
		goParamTypes: goParamTypes,
		structFields: structFields,
	}
}

// buildApplyCallContext creates a callContext for a companion-Apply call
// (e.g., `Try[string](() => ...)`), carrying the Apply method's metadata
// and the type-parameter substitutions derived from the call site.
func (t *galaASTTransformer) buildApplyCallContext(
	applyMeta *transpiler.MethodMetadata,
	applyTypeSubst map[string]string,
) callContext {
	return callContext{
		applyMethodMeta: applyMeta,
		applyTypeSubst:  applyTypeSubst,
	}
}

// resolveExpectedArgType resolves the expected type for a positional argument
// at the given index. The resolution logic depends on the call kind:
//
//   - For method calls with unresolved type params: only void FuncTypes pass through
//   - For method calls with resolved type params: substitute type params in param types
//   - For companion-Apply calls (Type[T](args)): substitute into Apply's param types
//   - For function calls: check GALA func metadata first (with type substitution for
//     generics), then Go type info, then struct field types
//
// Returns NilType if no expected type can be determined.
func (t *galaASTTransformer) resolveExpectedArgType(ctx callContext, argIdx int) transpiler.Type {
	// Method call path
	if ctx.methodMeta != nil {
		if ctx.unresolvedTP {
			// Only pass void function types (avoids unresolved type params in return types)
			if argIdx < len(ctx.methodMeta.ParamTypes) {
				if ft, ok := ctx.methodMeta.ParamTypes[argIdx].(transpiler.FuncType); ok && len(ft.Results) == 0 {
					return ft
				}
			}
			return transpiler.NilType{}
		}
		// Resolved type params — substitute and return
		if argIdx < len(ctx.methodMeta.ParamTypes) {
			return t.substituteTranspilerTypeParams(ctx.methodMeta.ParamTypes[argIdx], ctx.typeSubst)
		}
		return transpiler.NilType{}
	}

	// Companion-Apply path: Type[T](args) where Type has Apply.  Only
	// propagate if we have concrete substitutions for the type params —
	// otherwise passing `func() T` with unresolved T through to the lambda
	// breaks downstream inference (the lambda would emit a literal `T`
	// return type instead of inferring from its body).
	if ctx.applyMethodMeta != nil && len(ctx.applyTypeSubst) > 0 && argIdx < len(ctx.applyMethodMeta.ParamTypes) {
		return t.substituteTranspilerTypeParams(ctx.applyMethodMeta.ParamTypes[argIdx], ctx.applyTypeSubst)
	}

	// Function call path
	return t.resolveExpectedFuncArgType(ctx, argIdx)
}

// resolveExpectedFuncArgType resolves the expected type for a positional argument
// in a function call. It cascades through: GALA func metadata -> Go type info -> struct fields.
func (t *galaASTTransformer) resolveExpectedFuncArgType(ctx callContext, argIdx int) transpiler.Type {
	var expectedType transpiler.Type = transpiler.NilType{}

	if ctx.funcMeta != nil && argIdx < len(ctx.funcMeta.ParamTypes) {
		if ft, ok := ctx.funcMeta.ParamTypes[argIdx].(transpiler.FuncType); ok {
			if len(ctx.typeSubst) > 0 {
				// Substitute inferred or explicit type args (both void and non-void)
				expectedType = t.substituteTranspilerTypeParams(ctx.funcMeta.ParamTypes[argIdx], ctx.typeSubst)
			} else if len(ft.Results) == 0 || len(ctx.funcMeta.TypeParams) == 0 {
				// Void function type or non-generic function — pass as-is
				expectedType = ft
			}
		}
	}

	// FIX-075: Fall back to Go type info for lambda expected types
	if expectedType.IsNil() && ctx.goParamTypes != nil && argIdx < len(ctx.goParamTypes) {
		if ft, ok := ctx.goParamTypes[argIdx].(transpiler.FuncType); ok {
			expectedType = ft
		}
	}

	// If this is struct construction and we have field type info, use it as fallback
	if expectedType.IsNil() && ctx.structFields != nil && argIdx < len(ctx.structFields) {
		if ft, ok := ctx.structFields[argIdx].(transpiler.FuncType); ok {
			expectedType = ft
		}
	}

	return expectedType
}

// resolveNamedArgExpectedType resolves the expected type for a named argument
// by matching the argument name to the parameter list. For method calls it uses
// methodMeta.ParamNames/ParamTypes with type substitution. For function calls
// it uses funcMeta.ParamNames/ParamTypes, or falls back to struct field types.
func (t *galaASTTransformer) resolveNamedArgExpectedType(ctx callContext, argName string) transpiler.Type {
	// Method call path
	if ctx.methodMeta != nil {
		for pi, pName := range ctx.methodMeta.ParamNames {
			if pName == argName && pi < len(ctx.methodMeta.ParamTypes) {
				return t.substituteTranspilerTypeParams(ctx.methodMeta.ParamTypes[pi], ctx.typeSubst)
			}
		}
		return transpiler.NilType{}
	}

	// Function call path — handled separately since function named args have
	// struct field and Go type info fallback, but these are resolved inline
	// in transformCallWithArgsCtx due to needing additional context (funcName, fun expr).
	// This branch covers the funcMeta case only.
	if ctx.funcMeta != nil && len(ctx.funcMeta.ParamNames) > 0 {
		for i, paramName := range ctx.funcMeta.ParamNames {
			if paramName == argName && i < len(ctx.funcMeta.ParamTypes) {
				if ft, ok := ctx.funcMeta.ParamTypes[i].(transpiler.FuncType); ok {
					return ft
				}
				break
			}
		}
	}

	return transpiler.NilType{}
}
