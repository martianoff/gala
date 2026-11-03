package lsp_test

import (
	"testing"

	"martianoff/gala/internal/noantlr"
)

// TestReadsNoANTLR keeps the lsp library off ANTLR's tree (noantlr.Check).
func TestReadsNoANTLR(t *testing.T) { noantlr.Check(t, "lsp", 15) }
