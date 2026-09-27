package gooracle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rulesOf(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Rule)
	}
	return out
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		rules []string
	}{
		{
			name: "clean",
			src: `package main

func f[T any](x T) T { return x }

func main() { _ = f[int](1) }`,
		},
		{
			name: "method type-param sentinel",
			src: `package main

func main() { var x Box[__mtp0]; _ = x }`,
			rules: []string{"method-type-param-sentinel"},
		},
		{
			name: "expected-void sentinel",
			src: `package main

func main() { var f func() __void__; _ = f }`,
			rules: []string{"expected-void-sentinel"},
		},
		{
			name: "line marker",
			src: `package main

func main() {
	__gala_line_3
}`,
			rules: []string{"line-marker"},
		},
		{
			name:  "invalid type",
			src:   "package main\n\nvar f func() invalid type\n",
			rules: []string{"go-types-invalid", "parse"},
		},
		{
			name:  "untyped constant type",
			src:   "package main\n\nvar x untyped int\n",
			rules: []string{"go-types-untyped", "parse"},
		},
		{
			name:  "void as a type",
			src:   "package main\n\nfunc f() void { return }\n",
			rules: []string{"void-type"},
		},
		{
			name: "void declared by the file is fine",
			src: `package main

type void struct{}

func f() void { return void{} }`,
		},
		{
			name:  "leak in a string or comment is not a leak",
			src:   "package main\n\n// __mtp0 invalid type\nvar s = \"__gala_line_3 void\"\n",
			rules: nil,
		},
		{
			name:  "parse error",
			src:   "package main\n\nfunc main() {\n",
			rules: []string{"parse"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rulesOf(Check("x.go", tt.src))
			if tt.rules == nil {
				assert.Empty(t, got)
				return
			}
			for _, r := range tt.rules {
				assert.Contains(t, got, r)
			}
		})
	}
}

func TestLeakRulesAreDocumented(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range LeakRules {
		require.NotEmpty(t, r.Name)
		require.NotEmpty(t, r.Why, r.Name)
		require.False(t, seen[r.Name], "duplicate rule %s", r.Name)
		seen[r.Name] = true
	}
}

func TestTypeCheckSkipsUnresolvedImports(t *testing.T) {
	imp := NewImporter(ImporterConfig{ModulePath: "example.com/m", Root: t.TempDir()})

	res := imp.TypeCheck("x.go", "package main\n\nimport \"github.com/x/y\"\n\nvar _ = y.Z\n")
	assert.Contains(t, res.Skipped, "github.com/x/y")

	res = imp.TypeCheck("x.go", "package main\n\nimport \"example.com/m/missing\"\n\nvar _ = missing.Z\n")
	assert.Contains(t, res.Skipped, "example.com/m/missing")
}

func TestTypeCheckReportsErrors(t *testing.T) {
	imp := NewImporter(ImporterConfig{})

	res := imp.TypeCheck("x.go", "package main\n\nfunc f() int { return \"s\" }\n")
	assert.Empty(t, res.Skipped)
	assert.NotEmpty(t, res.Errors)

	res = imp.TypeCheck("x.go", "package main\n\nfunc main() { x := 1 }\n")
	assert.Empty(t, res.Errors, "unused variables are soft")
	assert.Equal(t, 1, res.Soft)

	res = imp.TypeCheck("x.go", "package main\n\nvar x V\n")
	assert.NotEmpty(t, res.Errors, "an uninstantiated type parameter is undefined")
}
