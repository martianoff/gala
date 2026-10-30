package commands

import (
	"os"
	"strings"
	"testing"
)

// TestParserFlagHelp checks that --parser's help names the engine the binary
// defaults to: galop, unless it is built with --//internal/parser:antlr. CI's
// job for ANTLR's parser builds so and expects ANTLR through
// GALA_EXPECT_DEFAULT_ENGINE.
func TestParserFlagHelp(t *testing.T) {
	want := os.Getenv("GALA_EXPECT_DEFAULT_ENGINE")
	if want == "" {
		want = "galop"
	}
	if help := parserFlagHelp(); !strings.HasSuffix(help, "else "+want+")") {
		t.Errorf("--parser help %q does not name %q as the default", help, want)
	}
}
