package analyzer_test

import (
	"testing"

	"martianoff/gala/internal/noantlr"
)

// TestReadsNoANTLR keeps the analyzer library off ANTLR's tree (noantlr.Check).
func TestReadsNoANTLR(t *testing.T) { noantlr.Check(t, "analyzer", 20) }
