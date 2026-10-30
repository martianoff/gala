package scopewalk

// This file stands in for walk_native.go in builds without galop's parser
// (--//internal/parser:antlr; see BUILD.bazel). Such a build has no native
// tree, so every walk is ANTLR's.

import "github.com/antlr4-go/antlr/v4"

func (w *Walker) walkNative(antlr.Tree) bool { return false }

func (w *Walker) walkParameterDefaultsNative(antlr.Tree) bool { return false }
