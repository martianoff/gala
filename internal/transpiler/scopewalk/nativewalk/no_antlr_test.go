package nativewalk_test

import (
	"testing"

	"martianoff/gala/internal/noantlr"
)

// TestReadsNoANTLR keeps the nativewalk library off ANTLR's tree (noantlr.Check).
func TestReadsNoANTLR(t *testing.T) { noantlr.Check(t, "nativewalk", 1) }
