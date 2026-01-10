package infer

import "fmt"

// Expr represents an expression in the Hindley-Milner system.
type Expr interface {
	fmt.Stringer
}

// Lit represents a literal expression.
type Lit struct {
	Value string
	Type  Type
}

func (e *Lit) String() string {
	return e.Value
}

// Var represents a variable expression.
type Var struct {
	Name string
}

func (e *Var) String() string {
	return e.Name
}

// App represents a function application expression.
type App struct {
	Fn  Expr
	Arg Expr
}

func (e *App) String() string {
	return fmt.Sprintf("(%s %s)", e.Fn, e.Arg)
}

// Abs represents a function abstraction (lambda) expression.
type Abs struct {
	Param string
	Body  Expr
}

func (e *Abs) String() string {
	return fmt.Sprintf("(\\%s -> %s)", e.Param, e.Body)
}

// Let represents a let-binding expression.
type Let struct {
	Name  string
	Value Expr
	Body  Expr
}

func (e *Let) String() string {
	return fmt.Sprintf("(let %s = %s in %s)", e.Name, e.Value, e.Body)
}
