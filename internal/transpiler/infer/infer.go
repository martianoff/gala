package infer

import (
	"fmt"

	"martianoff/gala/galaerr"
)

// codedInferError builds a SemanticError with no source span. The infer
// package operates on its own AST that does not carry GALA source
// positions; downstream callers wrap the error with a real span via
// `galaerr.NewSemanticErrorAt` when they have one. The code makes the
// failure family stable for tools and CI.
func codedInferError(code galaerr.ErrorCode, msg, hint string) error {
	return galaerr.NewCodedSemanticError(code, 0, 0, msg, hint)
}

const typeMismatchHint = "the expression's type does not match what the surrounding context expects — annotate the binding or convert the value explicitly"

// TypeEnv is a mapping from variable names to type schemes.
type TypeEnv map[string]*Scheme

func (e TypeEnv) Apply(s Substitution) TypeEnv {
	res := make(TypeEnv)
	for k, v := range e {
		res[k] = v.Apply(s)
	}
	return res
}

func (e TypeEnv) FreeTypeVars() map[*TypeVariable]bool {
	res := make(map[*TypeVariable]bool)
	for _, v := range e {
		for k, val := range v.FreeTypeVars() {
			res[k] = val
		}
	}
	return res
}

// Inferer holds the state for type inference.
type Inferer struct {
	nextID int
}

func NewInferer() *Inferer {
	return &Inferer{nextID: 0}
}

func (inf *Inferer) NewTypeVar() *TypeVariable {
	inf.nextID++
	return &TypeVariable{ID: inf.nextID}
}

func (inf *Inferer) instantiate(s *Scheme) Type {
	sub := make(Substitution)
	for _, v := range s.Vars {
		sub[v] = inf.NewTypeVar()
	}
	return s.Type.Apply(sub)
}

func (inf *Inferer) generalize(env TypeEnv, t Type) *Scheme {
	freeInEnv := env.FreeTypeVars()
	freeInType := t.FreeTypeVars()

	var vars []*TypeVariable
	for v := range freeInType {
		if !freeInEnv[v] {
			vars = append(vars, v)
		}
	}
	return &Scheme{Vars: vars, Type: t}
}

func (inf *Inferer) unify(t1, t2 Type) (Substitution, error) {
	switch a := t1.(type) {
	case *TypeVariable:
		return inf.bind(a, t2)
	}

	switch b := t2.(type) {
	case *TypeVariable:
		return inf.bind(b, t1)
	}

	if a, ok := t1.(*TypeApp); ok {
		if b, ok := t2.(*TypeApp); ok {
			if a.Name != b.Name || len(a.Args) != len(b.Args) {
				return nil, codedInferError(galaerr.CodeTypeMismatch,
					fmt.Sprintf("cannot unify %s and %s", a, b), typeMismatchHint)
			}
			s := make(Substitution)
			for i := 0; i < len(a.Args); i++ {
				s2, err := inf.unify(a.Args[i].Apply(s), b.Args[i].Apply(s))
				if err != nil {
					return nil, err
				}
				s = s.Compose(s2)
			}
			return s, nil
		}
	}

	if a, ok := t1.(*TypeConst); ok {
		if b, ok := t2.(*TypeConst); ok {
			if a.Name == b.Name {
				return make(Substitution), nil
			}
		}
	}

	return nil, codedInferError(galaerr.CodeTypeMismatch,
		fmt.Sprintf("cannot unify %s and %s", t1, t2), typeMismatchHint)
}

func (inf *Inferer) bind(v *TypeVariable, t Type) (Substitution, error) {
	if v == t {
		return make(Substitution), nil
	}
	if t.FreeTypeVars()[v] {
		return nil, codedInferError(galaerr.CodeOccursCheck,
			fmt.Sprintf("occurs check failed: %s in %s", v, t),
			"the inferred type would have to refer to itself; add an explicit type annotation or restructure the recursion")
	}
	return Substitution{v: t}, nil
}

func (inf *Inferer) Infer(env TypeEnv, e Expr) (Type, error) {
	t, _, err := inf.infer(env, e)
	return t, err
}

func (inf *Inferer) infer(env TypeEnv, e Expr) (Type, Substitution, error) {
	switch expr := e.(type) {
	case *Lit:
		return expr.Type, make(Substitution), nil

	case *Var:
		if s, ok := env[expr.Name]; ok {
			return inf.instantiate(s), make(Substitution), nil
		}
		return nil, nil, codedInferError(galaerr.CodeUndefinedVariable,
			fmt.Sprintf("undefined variable: %s", expr.Name),
			"check the spelling, ensure the import that introduces it is in scope, and verify the binding is reachable from the reference site")

	case *Abs:
		tv := inf.NewTypeVar()
		newEnv := make(TypeEnv)
		for k, v := range env {
			newEnv[k] = v
		}
		newEnv[expr.Param] = &Scheme{Type: tv}
		t, s, err := inf.infer(newEnv, expr.Body)
		if err != nil {
			return nil, nil, err
		}
		return (&TypeApp{Name: "->", Args: []Type{tv.Apply(s), t}}), s, nil

	case *App:
		tv := inf.NewTypeVar()
		tFn, s1, err := inf.infer(env, expr.Fn)
		if err != nil {
			return nil, nil, err
		}
		tArg, s2, err := inf.infer(env.Apply(s1), expr.Arg)
		if err != nil {
			return nil, nil, err
		}
		s3, err := inf.unify(tFn.Apply(s2), &TypeApp{Name: "->", Args: []Type{tArg, tv}})
		if err != nil {
			return nil, nil, err
		}
		return tv.Apply(s3), s3.Compose(s2).Compose(s1), nil

	case *Let:
		tVal, s1, err := inf.infer(env, expr.Value)
		if err != nil {
			return nil, nil, err
		}
		newEnv := env.Apply(s1)
		scheme := inf.generalize(newEnv, tVal)
		newEnv[expr.Name] = scheme
		tBody, s2, err := inf.infer(newEnv, expr.Body)
		if err != nil {
			return nil, nil, err
		}
		return tBody, s2.Compose(s1), nil
	case *If:
		tCond, s1, err := inf.infer(env, expr.Cond)
		if err != nil {
			return nil, nil, err
		}
		s2, err := inf.unify(tCond, &TypeConst{Name: "bool"})
		if err != nil {
			return nil, nil, codedInferError(galaerr.CodeTypeMismatch,
				fmt.Sprintf("if condition must be bool, got %s", tCond), typeMismatchHint)
		}
		newEnv := env.Apply(s2).Apply(s1)
		tThen, s3, err := inf.infer(newEnv, expr.Then)
		if err != nil {
			return nil, nil, err
		}
		tElse, s4, err := inf.infer(newEnv.Apply(s3), expr.Else)
		if err != nil {
			return nil, nil, err
		}
		s5, err := inf.unify(tThen.Apply(s4), tElse)
		if err != nil {
			return nil, nil, codedInferError(galaerr.CodeTypeMismatch,
				fmt.Sprintf("if branches must have same type: %s and %s", tThen.Apply(s4), tElse), typeMismatchHint)
		}
		return tThen.Apply(s4).Apply(s5), s5.Compose(s4).Compose(s3).Compose(s2).Compose(s1), nil
	}

	return nil, nil, codedInferError(galaerr.CodeInternalInferenceFailure,
		fmt.Sprintf("unknown expression type: %T", e),
		"please file an issue at https://github.com/martianoff/gala/issues with the source that triggered this failure")
}
