package infer

import (
	"fmt"
	"strings"
)

// Type represents a type in the Hindley-Milner system.
type Type interface {
	fmt.Stringer
	Apply(s Substitution) Type
	FreeTypeVars() map[*TypeVariable]bool
}

// TypeVariable represents a type variable (e.g., 'a, 'b).
type TypeVariable struct {
	ID int
}

func (t *TypeVariable) String() string {
	return fmt.Sprintf("t%d", t.ID)
}

func (t *TypeVariable) Apply(s Substitution) Type {
	if next, ok := s[t]; ok {
		return next.Apply(s)
	}
	return t
}

func (t *TypeVariable) FreeTypeVars() map[*TypeVariable]bool {
	return map[*TypeVariable]bool{t: true}
}

// TypeConst represents a type constant (e.g., int, string, bool).
type TypeConst struct {
	Name string
}

func (t *TypeConst) String() string {
	return t.Name
}

func (t *TypeConst) Apply(s Substitution) Type {
	return t
}

func (t *TypeConst) FreeTypeVars() map[*TypeVariable]bool {
	return map[*TypeVariable]bool{}
}

// TypeApp represents a type application (e.g., f a, Immutable[int]).
type TypeApp struct {
	Name string
	Args []Type
}

func (t *TypeApp) String() string {
	if len(t.Args) == 0 {
		return t.Name
	}
	args := make([]string, len(t.Args))
	for i, arg := range t.Args {
		args[i] = arg.String()
	}
	if t.Name == "->" {
		return fmt.Sprintf("(%s -> %s)", args[0], args[1])
	}
	return fmt.Sprintf("%s[%s]", t.Name, strings.Join(args, ", "))
}

func (t *TypeApp) Apply(s Substitution) Type {
	newArgs := make([]Type, len(t.Args))
	for i, arg := range t.Args {
		newArgs[i] = arg.Apply(s)
	}
	return &TypeApp{Name: t.Name, Args: newArgs}
}

func (t *TypeApp) FreeTypeVars() map[*TypeVariable]bool {
	res := make(map[*TypeVariable]bool)
	for _, arg := range t.Args {
		for k, v := range arg.FreeTypeVars() {
			res[k] = v
		}
	}
	return res
}

// Substitution is a mapping from type variables to types.
type Substitution map[*TypeVariable]Type

func (s Substitution) Compose(other Substitution) Substitution {
	res := make(Substitution)
	for k, v := range other {
		res[k] = v.Apply(s)
	}
	for k, v := range s {
		res[k] = v
	}
	return res
}

// Scheme represents a type scheme (quantified type).
type Scheme struct {
	Vars []*TypeVariable
	Type Type
}

func (s *Scheme) Apply(sub Substitution) *Scheme {
	newSub := make(Substitution)
	for k, v := range sub {
		newSub[k] = v
	}
	for _, v := range s.Vars {
		delete(newSub, v)
	}
	return &Scheme{
		Vars: s.Vars,
		Type: s.Type.Apply(newSub),
	}
}

func (s *Scheme) FreeTypeVars() map[*TypeVariable]bool {
	res := s.Type.FreeTypeVars()
	for _, v := range s.Vars {
		delete(res, v)
	}
	return res
}

func (s *Scheme) String() string {
	if len(s.Vars) == 0 {
		return s.Type.String()
	}
	vars := make([]string, len(s.Vars))
	for i, v := range s.Vars {
		vars[i] = v.String()
	}
	return fmt.Sprintf("forall %s. %s", strings.Join(vars, ", "), s.Type.String())
}
