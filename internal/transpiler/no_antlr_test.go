package transpiler_test

import (
	"testing"

	"martianoff/gala/internal/noantlr"
)

// TestReadsNoANTLR keeps the transpiler library off ANTLR's tree (noantlr.Check).
func TestReadsNoANTLR(t *testing.T) { noantlr.Check(t, "transpiler", 5) }
