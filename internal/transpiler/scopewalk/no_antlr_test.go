package scopewalk_test

import (
	"testing"

	"martianoff/gala/internal/noantlr"
)

// TestReadsNoANTLR keeps the scopewalk library off ANTLR's tree (noantlr.Check).
func TestReadsNoANTLR(t *testing.T) { noantlr.Check(t, "scopewalk", 1) }
