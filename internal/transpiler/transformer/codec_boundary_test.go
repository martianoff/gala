package transformer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCodec_NoJsonSpecificHardcodingOutsideTypedCodegen guards the Option-C
// codec architecture: the transpiler must not reach into JSON-specific method
// names (WriteString, ReadInt, BeginObject, etc.) as fixed string literals
// anywhere outside the typed codegen dispatcher in codec_typed.go. All such
// references belong in std/json/codec.gala's FieldEncoder/FieldDecoder impls.
//
// If this test fails, the transpiler has likely regressed and reintroduced
// format-specific logic. The fix is to move that logic into a library
// (std/json/codec.gala or a new format peer) and have the transpiler emit
// only calls against the neutral FieldEncoder / FieldDecoder interfaces.
func TestCodec_NoJsonSpecificHardcodingOutsideTypedCodegen(t *testing.T) {
	// Method names that belong exclusively to the FieldEncoder / FieldDecoder
	// interfaces — they are meaningful only at the boundary between the
	// transpiler (which emits calls to them) and the library (which
	// implements them).
	forbidden := []string{
		`"WriteString"`,
		`"WriteInt"`,
		`"WriteInt64"`,
		`"WriteFloat64"`,
		`"WriteBool"`,
		`"WriteRune"`,
		`"WriteNull"`,
		`"WriteKey"`,
		`"WriteStartObject"`,
		`"WriteEndObject"`,
		`"WriteStartArray"`,
		`"WriteEndArray"`,
		`"ReadString"`,
		`"ReadInt64"`,
		`"ReadFloat64"`,
		`"ReadBool"`,
		`"ReadRune"`,
		`"ReadNull"`,
		// NOTE: "ReadInt", "ReadKey" also live in codec_typed.go but may
		// conflict with unrelated helpers in the transpiler (std.EmbeddedFS
		// has a ReadString method, for instance). We rely on the negative
		// list below to whitelist the typed codegen file.
		`"BeginObject"`,
		`"EndObject"`,
		`"BeginArray"`,
		`"EndArray"`,
	}

	// Files allowed to reference these names.  codec_typed.go is the sole
	// owner of Option-C typed dispatch emission; test files obviously
	// reference them as part of coverage assertions.
	allowed := func(path string) bool {
		base := filepath.Base(path)
		if base == "codec_typed.go" {
			return true
		}
		if strings.HasSuffix(base, "_test.go") {
			return true
		}
		return false
	}

	root := "."
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if allowed(path) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := string(data)
		for _, needle := range forbidden {
			if strings.Contains(content, needle) {
				t.Errorf("%s: contains forbidden JSON-specific method literal %s — move format-specific dispatch into std/json/codec.gala", path, needle)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk failed: %v", err)
	}
}
