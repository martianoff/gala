// Package nativewalk, in this directory, stands in for
// internal/transpiler/scopewalk/nativewalk in builds without galop's parser
// (--//internal/parser:antlr, or nix with useGalop = false), which have no
// native tree: For gives no NativeWalker, so every scope walk is ANTLR's.
package nativewalk

import (
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/transpiler/scopewalk"
)

// For is nil: there is no native tree to walk.
func For(*parser.Parsed) scopewalk.NativeWalker { return nil }
