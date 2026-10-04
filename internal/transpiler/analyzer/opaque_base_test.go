package analyzer

import (
	"testing"

	"martianoff/gala/internal/transpiler"
)

// The analyzer records an opaque type's underlying type followed through the
// declaring file's aliases, in main and in a library, where a file's own
// aliases are not in the bare TypeAliases keys.
func TestOpaqueUnderlyingBase(t *testing.T) {
	for _, tt := range []struct {
		name, src, key, want string
	}{
		{
			name: "main, alias in the same file",
			src:  "package main\n\ntype Switch bool\n\nopaque type Flag Switch\n\nfunc main() {}\n",
			key:  "Flag", want: "bool",
		},
		{
			name: "library, alias chain",
			src:  "package cfg\n\ntype Raw int64\n\ntype Level Raw\n\nopaque type Verbosity Level\n",
			key:  "cfg.Verbosity", want: "int64",
		},
		{
			name: "Go named scalar",
			src:  "package cfg\n\nimport \"time\"\n\nopaque type Timeout time.Duration\n",
			key:  "cfg.Timeout", want: "int64",
		},
		{
			name: "primitive",
			src:  "package cfg\n\nopaque type UserID int64\n",
			key:  "cfg.UserID", want: "int64",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			meta := analyzeSrc(t, tt.src).Types[tt.key]
			if meta == nil {
				t.Fatalf("no metadata for %s", tt.key)
			}
			if got := typeName(meta.UnderlyingBase); got != tt.want {
				t.Errorf("UnderlyingBase = %s, want %s", got, tt.want)
			}
		})
	}
}

func typeName(typ transpiler.Type) string {
	if typ == nil {
		return "<nil>"
	}
	return typ.String()
}
