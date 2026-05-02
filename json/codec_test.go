package json

import (
	"strings"
	"testing"
)

// ----- Decoder unit tests --------------------------------------------------
// These tests drive JsonDecoderImpl through the same call sequence the
// transpiler emits for transpiler-generated _StructMeta_T.DecodeFields.
// The end-to-end shape is a JSON object whose interior contains a nested
// object that itself contains an array of structs, followed by trailing
// fields — this is the shape of stream-json `assistant` events emitted by
// `claude --print --output-format stream-json --verbose`. A bug in the
// decoder's string-reading or comma-consumption state machine on long
// `text` fields would manifest as the extracted string carrying envelope
// tokens like `"}],"stop_reason":"end_turn",...` glued to the end.

func TestDecoder_ClaudeAssistantShape_Small(t *testing.T) {
	// Minimal claude-shape event. The transpiler-generated DecodeFields for
	// ClaudeAssistantEvent visits keys in declaration order; on each key it
	// dispatches to ReadKey/ReadString/Skip/StartObject/etc.
	src := `{"type":"assistant","message":{"id":"msg_01","content":[{"type":"text","text":"hello world"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":5,"output_tokens":10,"cache_read_input_tokens":0}}}`

	d := NewJsonDecoder(src)
	_, _, _, gotText, gotStopReason, gotInputTokens := decodeClaudeAssistant(d)

	if gotText != "hello world" {
		t.Fatalf("text = %q (want %q)", gotText, "hello world")
	}
	if gotStopReason != "end_turn" {
		t.Fatalf("stop_reason = %q (want end_turn)", gotStopReason)
	}
	if gotInputTokens != 5 {
		t.Fatalf("input_tokens = %d (want 5)", gotInputTokens)
	}
}

// TestDecoder_ClaudeAssistantShape_LongText is the headline reproducer.
// Build a `text` field with 8 KiB of mixed content (paragraphs, fences,
// quotes, backslashes, embedded newlines as `\n`) and verify the decoded
// string is byte-identical to the input — no leak of trailing envelope
// tokens like `"}],"stop_reason":...`.
func TestDecoder_ClaudeAssistantShape_LongText(t *testing.T) {
	original := buildLongAssistantText(8 * 1024)
	src := wrapInClaudeEnvelope(original)

	d := NewJsonDecoder(src)
	_, _, _, gotText, gotStopReason, gotInputTokens := decodeClaudeAssistant(d)

	if gotText != original {
		t.Fatalf("decoded text length=%d, original length=%d\nfirst diff at byte %d\ndecoded tail: %q\noriginal tail: %q",
			len(gotText), len(original), firstDiff(gotText, original),
			tail(gotText, 80), tail(original, 80))
	}
	if gotStopReason != "end_turn" {
		t.Fatalf("stop_reason = %q (want end_turn) — codec drifted past envelope boundary", gotStopReason)
	}
	if gotInputTokens != 1234 {
		t.Fatalf("input_tokens = %d (want 1234)", gotInputTokens)
	}
}

func TestDecoder_ClaudeAssistantShape_HugeText(t *testing.T) {
	original := buildLongAssistantText(64 * 1024)
	src := wrapInClaudeEnvelope(original)

	d := NewJsonDecoder(src)
	_, _, _, gotText, _, _ := decodeClaudeAssistant(d)

	if gotText != original {
		t.Fatalf("decoded text length=%d, original length=%d\nfirst diff at byte %d\ndecoded tail: %q",
			len(gotText), len(original), firstDiff(gotText, original), tail(gotText, 120))
	}
}

func TestDecoder_StringWithBackslashHeavy(t *testing.T) {
	// Backslash density stress: many `\\` and `\"` mixed in.  If the escape
	// counter is off-by-one the closing quote would be missed and the
	// remainder of the JSON would be glued onto the string.
	var b strings.Builder
	for i := 0; i < 4000; i++ {
		b.WriteString(`code: \\path\\to\\file`)
		if i%5 == 0 {
			b.WriteString(` quote: \"hello\"`)
		}
		b.WriteString(`\n`)
	}
	original := b.String()
	src := wrapInClaudeEnvelopeRaw(original)

	d := NewJsonDecoder(src)
	_, _, _, gotText, gotStopReason, _ := decodeClaudeAssistant(d)

	wantText := decodeRawJSONString(original)
	if gotText != wantText {
		t.Fatalf("decoded text mismatch (escape stress):\nlen got=%d want=%d\nfirst diff @ %d\ngot tail: %q",
			len(gotText), len(wantText), firstDiff(gotText, wantText), tail(gotText, 80))
	}
	if gotStopReason != "end_turn" {
		t.Fatalf("stop_reason = %q (envelope drift on backslash-heavy input)", gotStopReason)
	}
}

// --- helpers ---------------------------------------------------------------

// decodeClaudeAssistant drives the decoder through the call sequence the
// transpiler emits for:
//
//	ClaudeAssistantEvent { Type string, Message ClaudeAssistantMsg }
//	ClaudeAssistantMsg   { Content Array[ClaudePart], Usage ClaudeUsage }
//	ClaudePart           { Type string, Text string }
//	ClaudeUsage          { InputTokens int, OutputTokens int, CacheReadInputTokens int }
//
// It returns (eventType, msgId-skipped, partType, partText, stopReason, inputTokens).
// We surface the keys we care about and Skip() the rest, mirroring what the
// transpiler-generated DecodeFields does for unknown keys (id, model, role,
// stop_sequence, output_tokens, etc).
func decodeClaudeAssistant(d *JsonDecoderImpl) (eventType, msgIdSkip, partType, partText, stopReason string, inputTokens int) {
	d.StartObject()
	for d.HasMoreFields() {
		k := d.ReadKey()
		switch k {
		case "type":
			eventType = d.ReadString()
		case "message":
			d.StartObject()
			for d.HasMoreFields() {
				mk := d.ReadKey()
				switch mk {
				case "content":
					d.StartArray()
					for d.HasMoreElements() {
						d.StartObject()
						for d.HasMoreFields() {
							pk := d.ReadKey()
							switch pk {
							case "type":
								partType = d.ReadString()
							case "text":
								partText = d.ReadString()
							default:
								d.Skip()
							}
						}
						d.EndObject()
					}
					d.EndArray()
				case "stop_reason":
					if d.IsNull() {
						d.ReadNull()
					} else {
						stopReason = d.ReadString()
					}
				case "usage":
					d.StartObject()
					for d.HasMoreFields() {
						uk := d.ReadKey()
						switch uk {
						case "input_tokens":
							inputTokens = d.ReadInt()
						default:
							d.Skip()
						}
					}
					d.EndObject()
				default:
					d.Skip()
				}
			}
			d.EndObject()
		default:
			d.Skip()
		}
	}
	d.EndObject()
	return
}

// buildLongAssistantText returns ~targetLen bytes of mixed claude-typical
// content: paragraphs, code fences (whose contents include `\n`, `"`,
// `\\`), markdown lists, and a few unicode runes — the kind of payload an
// 8KB+ assistant response actually has. The returned string is the *raw*
// (un-escaped) text — what the user would observe; wrapInClaudeEnvelope
// JSON-escapes it before feeding to the decoder.
func buildLongAssistantText(targetLen int) string {
	const block = "Here is a paragraph of explanatory text that mentions several details: a path like /usr/local/bin, an env var $HOME, a number 42, and a quote \"like this\". " +
		"```go\nfunc main() {\n\tfmt.Println(\"hello, world\")\n\t// path: C:\\\\Users\\\\foo\\n}\n```\n" +
		"- bullet one with `inline code`\n- bullet two with [a link](https://example.com)\n- bullet three has trailing backslash \\\\\n\n" +
		"And one more paragraph mentioning end_turn, stop_reason, usage, input_tokens — substrings the prior heuristic falsely flagged as envelope tokens. They are legitimate words in many responses.\n\n"
	var b strings.Builder
	for b.Len() < targetLen {
		b.WriteString(block)
	}
	return b.String()
}

// wrapInClaudeEnvelope produces a JSON object string structurally identical
// to a real `claude --output-format stream-json` `assistant` event. The
// `text` field holds JSON-escaped `original`. Trailing fields after
// `content[]` are exactly the ones the prior sanitiser flagged.
func wrapInClaudeEnvelope(original string) string {
	return `{"type":"assistant","message":{"id":"msg_01ABCDEF","model":"claude-opus-4-5","role":"assistant","content":[{"type":"text","text":"` +
		jsonEscape(original) +
		`"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1234,"output_tokens":5678,"cache_read_input_tokens":0}}}`
}

// wrapInClaudeEnvelopeRaw embeds an already-JSON-escaped payload (caller has
// pre-escaped backslashes / quotes). Used for backslash-stress tests where
// we want explicit control over the escape sequences in the wire bytes.
func wrapInClaudeEnvelopeRaw(rawBody string) string {
	return `{"type":"assistant","message":{"content":[{"type":"text","text":"` +
		rawBody +
		`"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":0}}}`
}

// jsonEscape produces the body bytes that go between the surrounding `"`s
// of a JSON string literal. We deliberately reimplement (vs. encoding/json)
// so the test fixture is fully self-describing.
func jsonEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20:
			b.WriteString(`\u00`)
			const hex = "0123456789abcdef"
			b.WriteByte(hex[(c>>4)&0xF])
			b.WriteByte(hex[c&0xF])
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// decodeRawJSONString applies `\\`, `\"`, `\n`, `\r`, `\t` substitutions to
// the raw escape-stress fixture so we can compute the expected decoded
// string without re-running the codec we're testing.
func decodeRawJSONString(raw string) string {
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == '\\' && i+1 < len(raw) {
			esc := raw[i+1]
			switch esc {
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(esc)
			}
			i++
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func firstDiff(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// TestDecoder_TextEndsWithEscapedBackslash exercises the corner where the
// raw text's last visible char is a single backslash. JSON-encoded that's
// `\\` immediately before the closing `"` — i.e. the wire bytes look like
// `...\\"` and a naive escape state-machine could mistake the closing
// quote for an escaped one (`\"`). We test sizes from tiny up to past
// 8 KiB so a buffered reader's chunk-boundary off-by-one would also fire.
func TestDecoder_TextEndsWithEscapedBackslash(t *testing.T) {
	for _, n := range []int{1, 2, 3, 64, 4093, 4094, 4095, 8190, 8191, 8192, 16384} {
		body := strings.Repeat("a", n) + "\\"
		src := wrapInClaudeEnvelope(body)
		d := NewJsonDecoder(src)
		_, _, _, gotText, gotStop, _ := decodeClaudeAssistant(d)
		if gotText != body {
			t.Fatalf("n=%d: text mismatch (len got=%d want=%d, tail=%q)",
				n, len(gotText), len(body), tail(gotText, 30))
		}
		if gotStop != "end_turn" {
			t.Fatalf("n=%d: stop_reason=%q (envelope drift)", n, gotStop)
		}
	}
}

// TestDecoder_TextEndsWithEscapedQuote: same but the last visible char is a
// `"` (escaped on the wire as `\"`).
func TestDecoder_TextEndsWithEscapedQuote(t *testing.T) {
	for _, n := range []int{0, 1, 1023, 1024, 4095, 4096, 8192, 16384} {
		body := strings.Repeat("z", n) + `"`
		src := wrapInClaudeEnvelope(body)
		d := NewJsonDecoder(src)
		_, _, _, gotText, gotStop, _ := decodeClaudeAssistant(d)
		if gotText != body {
			t.Fatalf("n=%d: text mismatch", n)
		}
		if gotStop != "end_turn" {
			t.Fatalf("n=%d: envelope drift", n)
		}
	}
}

// TestDecoder_UnicodeEscapesDense: many `\u00xx` escapes packed in. Tests
// the +4 hex consumer in readJsonString.
func TestDecoder_UnicodeEscapesDense(t *testing.T) {
	// Build raw escape body: 5000 occurrences of `A` (== "A").
	rawBody := strings.Repeat(`A`, 5000)
	want := strings.Repeat("A", 5000)
	src := wrapInClaudeEnvelopeRaw(rawBody)
	d := NewJsonDecoder(src)
	_, _, _, gotText, gotStop, _ := decodeClaudeAssistant(d)
	if gotText != want {
		t.Fatalf("text mismatch: len got=%d want=%d", len(gotText), len(want))
	}
	if gotStop != "end_turn" {
		t.Fatalf("envelope drift: stop_reason=%q", gotStop)
	}
}

// TestDecoder_AdjacentEscapedQuoteAndBackslash: pairs that look ambiguous
// to a naive escape-state-machine.  The wire form `\\\"` is "literal
// backslash followed by literal quote" — easy to misparse as "literal
// backslash, then end of string, then garbage".
func TestDecoder_AdjacentEscapedQuoteAndBackslash(t *testing.T) {
	// 4000 copies of (raw) `\\\"` — that's `\` + `"` after JSON unescape.
	rawBody := strings.Repeat(`\\\"`, 4000)
	want := strings.Repeat(`\"`, 4000)
	src := wrapInClaudeEnvelopeRaw(rawBody)
	d := NewJsonDecoder(src)
	_, _, _, gotText, gotStop, _ := decodeClaudeAssistant(d)
	if gotText != want {
		t.Fatalf("text mismatch: len got=%d want=%d, tail=%q",
			len(gotText), len(want), tail(gotText, 40))
	}
	if gotStop != "end_turn" {
		t.Fatalf("envelope drift: stop_reason=%q", gotStop)
	}
}

// TestDecoder_TwoEventsConcatenated documents what happens when the *caller*
// (e.g. an upstream bufio.Scanner with a too-small buffer) feeds two
// concatenated JSON events to a single Decode() call.  The decoder MUST
// consume only the first event — leaving the second event un-touched in
// the buffer is acceptable; bleeding the second event's bytes into a
// string field would be the bug.  This is the shape that motivated the
// downstream `sanitizeAssistantText` heuristic.
func TestDecoder_TwoEventsConcatenated(t *testing.T) {
	first := wrapInClaudeEnvelope("hello from event 1")
	second := wrapInClaudeEnvelope("hello from event 2")
	src := first + second // no separator — what a broken Scanner might yield

	d := NewJsonDecoder(src)
	_, _, _, gotText, gotStop, _ := decodeClaudeAssistant(d)
	if gotText != "hello from event 1" {
		t.Fatalf("text = %q (want only the first event's text)", gotText)
	}
	if gotStop != "end_turn" {
		t.Fatalf("stop_reason = %q", gotStop)
	}
	// Decoder is at the first byte of the second event (`{`).  The second
	// event is then independently decodable.
	d2 := NewJsonDecoder(src[d.pos:])
	_, _, _, gotText2, _, _ := decodeClaudeAssistant(d2)
	if gotText2 != "hello from event 2" {
		t.Fatalf("second event text = %q", gotText2)
	}
}

// TestDecoder_TrailingBackslashEscapedQuote: text that ends with `\"`
// (escaped quote) — the close `"` follows immediately, so the wire form
// near the end is `...\\\""` (literal-bs, literal-quote, then JSON close).
func TestDecoder_TrailingBackslashEscapedQuote(t *testing.T) {
	for _, n := range []int{0, 1023, 8191, 65535} {
		body := strings.Repeat("x", n) + `\"`
		src := wrapInClaudeEnvelope(body)
		d := NewJsonDecoder(src)
		_, _, _, gotText, gotStop, _ := decodeClaudeAssistant(d)
		if gotText != body {
			t.Fatalf("n=%d: text mismatch (len got=%d want=%d)", n, len(gotText), len(body))
		}
		if gotStop != "end_turn" {
			t.Fatalf("n=%d: envelope drift", n)
		}
	}
}
