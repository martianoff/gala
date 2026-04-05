#!/usr/bin/env bash
# Copies ANTLR-generated Java sources into the IntelliJ plugin's gen directory.
# Usage: bazel run //internal/parser/grammar:copy_java_to_ide
set -euo pipefail

WORKSPACE="${BUILD_WORKSPACE_DIRECTORY:-.}"
TARGET_DIR="$WORKSPACE/ide/intellij/src/main/gen/org/gala/ide/intellij/parser"

mkdir -p "$TARGET_DIR"

# Remove old read-only files (Bazel outputs are read-only)
rm -f "$TARGET_DIR"/*.java 2>/dev/null || true

RUNFILES_DIR="${RUNFILES_DIR:-$0.runfiles}"
GRAMMAR_DIR="$RUNFILES_DIR/_main/internal/parser/grammar"

for f in galaBaseListener.java galaBaseVisitor.java galaLexer.java galaListener.java galaParser.java galaVisitor.java; do
    if [ -f "$GRAMMAR_DIR/$f" ]; then
        cp "$GRAMMAR_DIR/$f" "$TARGET_DIR/$f"
        chmod u+w "$TARGET_DIR/$f"
        echo "Copied $f"
    else
        echo "WARNING: $f not found in $GRAMMAR_DIR" >&2
    fi
done

echo "Done. Generated Java sources are in $TARGET_DIR"
