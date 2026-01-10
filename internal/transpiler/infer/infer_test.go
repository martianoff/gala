package infer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInfer(t *testing.T) {
	intType := &TypeConst{Name: "int"}
	stringType := &TypeConst{Name: "string"}

	tv100 := &TypeVariable{ID: 100}

	tests := []struct {
		name    string
		env     TypeEnv
		expr    Expr
		want    string
		wantErr bool
	}{
		{
			name: "literal int",
			env:  make(TypeEnv),
			expr: &Lit{Value: "1", Type: intType},
			want: "int",
		},
		{
			name: "variable lookup",
			env: TypeEnv{
				"x": &Scheme{Type: intType},
			},
			expr: &Var{Name: "x"},
			want: "int",
		},
		{
			name: "function application",
			env: TypeEnv{
				"f": &Scheme{Type: &TypeApp{Name: "->", Args: []Type{intType, stringType}}},
				"x": &Scheme{Type: intType},
			},
			expr: &App{Fn: &Var{Name: "f"}, Arg: &Var{Name: "x"}},
			want: "string",
		},
		{
			name: "lambda abstraction",
			env:  make(TypeEnv),
			expr: &Abs{Param: "x", Body: &Var{Name: "x"}},
			want: "(t1 -> t1)",
		},
		{
			name: "polymorphic let",
			env:  make(TypeEnv),
			expr: &Let{
				Name:  "id",
				Value: &Abs{Param: "x", Body: &Var{Name: "x"}},
				Body: &App{
					Fn:  &Var{Name: "id"},
					Arg: &Lit{Value: "1", Type: intType},
				},
			},
			want: "int",
		},
		{
			name: "polymorphic let multiple use",
			env:  make(TypeEnv),
			expr: &Let{
				Name:  "id",
				Value: &Abs{Param: "x", Body: &Var{Name: "x"}},
				Body: &Let{
					Name: "ignore",
					Value: &App{
						Fn:  &Var{Name: "id"},
						Arg: &Lit{Value: "1", Type: intType},
					},
					Body: &App{
						Fn:  &Var{Name: "id"},
						Arg: &Lit{Value: "foo", Type: stringType},
					},
				},
			},
			want: "string",
		},
		{
			name: "Immutable type application",
			env: TypeEnv{
				"x": &Scheme{Type: intType},
				"newImmutable": &Scheme{
					Vars: []*TypeVariable{tv100},
					Type: &TypeApp{
						Name: "->",
						Args: []Type{
							tv100,
							&TypeApp{Name: "Immutable", Args: []Type{tv100}},
						},
					},
				},
			},
			expr: &App{Fn: &Var{Name: "newImmutable"}, Arg: &Var{Name: "x"}},
			want: "Immutable[int]",
		},
		{
			name:    "unbound variable",
			env:     make(TypeEnv),
			expr:    &Var{Name: "y"},
			wantErr: true,
		},
		{
			name: "unification failure",
			env: TypeEnv{
				"f": &Scheme{Type: &TypeApp{Name: "->", Args: []Type{intType, stringType}}},
				"x": &Scheme{Type: stringType},
			},
			expr:    &App{Fn: &Var{Name: "f"}, Arg: &Var{Name: "x"}},
			wantErr: true,
		},
		{
			name:    "occurs check",
			env:     make(TypeEnv),
			expr:    &Abs{Param: "f", Body: &App{Fn: &Var{Name: "f"}, Arg: &Var{Name: "f"}}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inf := NewInferer()
			got, err := inf.Infer(tt.env, tt.expr)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got.String())
			}
		})
	}
}
