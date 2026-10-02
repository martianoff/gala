// Package yaml — YAML codec implementation (encoder + decoder).
//
// This file holds the parser and emitter machinery in plain Go. The
// GALA-side files (yaml.gala, naming.gala, helpers.gala) provide the
// Codec[T] / Naming / KeyConfig surface that uses the StructMeta[T]
// auto-injection pattern, mirroring the structure of gala_simple/json/.
//
// Why split this way: the byte/string slicing and multi-return helper
// shapes the parser needs aren't supported by GALA's parser, and the
// json package follows the same precedent (its codec.gala is the
// orchestrator; byte_utils.go carries the low-level conversions).
package yaml

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"martianoff/gala/std"
)

// ============================================================================
// YamlEncoderImpl — implements std.FieldEncoder for block-style YAML output.
//
// Block style is the form humans actually want to read:
//
//	project:
//	  name: auth-service
//	  repo: .
//	team:
//	  name: Skunkworks
//	  members:
//	    - role: team_lead
//	      name: Iris
//
// Strings are emitted unquoted when unambiguously plain (no reserved leading
// character, no embedded ": ", no leading/trailing space, not parseable as a
// number or bool); otherwise they get a double-quoted form with standard
// backslash escapes. Multi-line strings always get quoted with "\n" escapes
// — the encoder never emits literal-block (`|`) scalars so the round-trip is
// fully unambiguous. The decoder still accepts `|` blocks from human-authored
// YAML.
// ============================================================================

type YamlEncoderImpl struct {
	buf   bytes.Buffer
	depth int    // current indent level (each level = 2 spaces)
	stack []byte // frame kinds: 'r' root, 'o' mapping, 'a' sequence
	// emptyMarks parallels stack: the buffer length just after each open
	// container's header ("key:", "- ", "-"); 0 for the document root.
	// A container that closes with the buffer still at its mark (plus the
	// header's newline) wrote no children, and is rewritten in flow style
	// ("key: []", "- {}") — block style has no spelling for an empty
	// container, and a bare "key:" reads back as null.
	emptyMarks    []int
	pendingKey    string
	hasKey        bool
	inlineNextKey bool // first key after "- " sits on the same line
	startedDoc    bool
}

func NewYamlEncoder() *YamlEncoderImpl { return &YamlEncoderImpl{} }

func (e *YamlEncoderImpl) String() string {
	s := e.buf.String()
	// Trim a single trailing newline so callers don't get a stray blank line.
	if len(s) > 0 && s[len(s)-1] == '\n' {
		return s[:len(s)-1]
	}
	return s
}

func (e *YamlEncoderImpl) topFrame() byte {
	if len(e.stack) == 0 {
		return 'r'
	}
	return e.stack[len(e.stack)-1]
}

func (e *YamlEncoderImpl) writeIndent() {
	for i := 0; i < e.depth; i++ {
		e.buf.WriteString("  ")
	}
}

// emitKeyHeader writes the "key:" prefix without a value or trailing newline.
// Used when the value is itself a nested mapping or sequence — caller is
// responsible for emitting the newline + bumping indent for the children.
func (e *YamlEncoderImpl) emitKeyHeader(key string) {
	if e.inlineNextKey {
		// First key of a mapping that lives on the same line as a sequence
		// dash ("- role: lead"). No leading indent, no leading newline.
		e.buf.WriteString(plainOrQuoted(key))
		e.buf.WriteByte(':')
		e.inlineNextKey = false
		return
	}
	e.writeIndent()
	e.buf.WriteString(plainOrQuoted(key))
	e.buf.WriteByte(':')
}

// startContainerPrelude handles the prelude common to StartObject and
// StartArray: emit any pending key header (or sequence-item dash) and adjust
// indent so the new container's children render at the right depth.
//
// It returns the buffer length just after the header, before any newline the
// header ends with; 0 for the document root, so an empty root container
// is written "[]" or "{}" (see emptyMarks).
func (e *YamlEncoderImpl) startContainerPrelude(isObject bool) int {
	parent := e.topFrame()
	if parent == 'r' && !e.startedDoc {
		// Document root — no prelude, depth stays 0.
		e.startedDoc = true
		return 0
	}
	if e.hasKey {
		// We're a value of a mapping pair: emit "key:\n", then indent children.
		e.emitKeyHeader(e.pendingKey)
		mark := e.buf.Len()
		e.buf.WriteByte('\n')
		e.depth++
		e.pendingKey = ""
		e.hasKey = false
		return mark
	}
	if parent == 'a' {
		// We're a list element that is itself a container.
		e.writeIndent()
		if isObject {
			// Compact form: "- " then the first key inline.
			e.buf.WriteString("- ")
			e.depth++
			e.inlineNextKey = true
			return e.buf.Len()
		}
		// Nested sequence in sequence: bare "-" line, indent inner.
		e.buf.WriteString("-")
		mark := e.buf.Len()
		e.buf.WriteByte('\n')
		e.depth++
		return mark
	}
	panic("yaml encoder: container started with no key in mapping context")
}

// endContainer pops the innermost container of the given kind. When it wrote
// no children, its header is rewritten in flow style with `flow` ("[]" or
// "{}") so the empty container reads back as empty rather than null.
func (e *YamlEncoderImpl) endContainer(kind byte, flow string) {
	if len(e.stack) == 0 || e.stack[len(e.stack)-1] != kind {
		panic(fmt.Sprintf("yaml encoder: end of %c container without a matching start", kind))
	}
	mark := e.emptyMarks[len(e.emptyMarks)-1]
	e.stack = e.stack[:len(e.stack)-1]
	e.emptyMarks = e.emptyMarks[:len(e.emptyMarks)-1]
	if e.buf.Len() <= mark+1 {
		e.buf.Truncate(mark)
		if e.buf.Len() > 0 && e.buf.Bytes()[e.buf.Len()-1] != ' ' {
			e.buf.WriteByte(' ')
		}
		e.buf.WriteString(flow)
		e.buf.WriteByte('\n')
	}
	if e.topFrame() != 'r' && e.depth > 0 {
		e.depth--
	}
}

func (e *YamlEncoderImpl) WriteStartObject() {
	mark := e.startContainerPrelude(true)
	e.stack = append(e.stack, 'o')
	e.emptyMarks = append(e.emptyMarks, mark)
}

func (e *YamlEncoderImpl) WriteEndObject() {
	e.endContainer('o', "{}")
	e.inlineNextKey = false
}

func (e *YamlEncoderImpl) WriteStartArray() {
	mark := e.startContainerPrelude(false)
	e.stack = append(e.stack, 'a')
	e.emptyMarks = append(e.emptyMarks, mark)
}

func (e *YamlEncoderImpl) WriteEndArray() {
	e.endContainer('a', "[]")
}

func (e *YamlEncoderImpl) WriteKey(name string) {
	e.pendingKey = name
	e.hasKey = true
}

// writeScalar is the shared body of every WriteString/WriteInt/etc.
// `value` is the already-stringified scalar (no surrounding context).
func (e *YamlEncoderImpl) writeScalar(value string) {
	if e.hasKey {
		e.emitKeyHeader(e.pendingKey)
		e.buf.WriteByte(' ')
		e.buf.WriteString(value)
		e.buf.WriteByte('\n')
		e.pendingKey = ""
		e.hasKey = false
		return
	}
	if e.topFrame() == 'a' {
		e.writeIndent()
		e.buf.WriteString("- ")
		e.buf.WriteString(value)
		e.buf.WriteByte('\n')
		return
	}
	// Root scalar — rare, but emit cleanly.
	e.buf.WriteString(value)
	e.buf.WriteByte('\n')
	e.startedDoc = true
}

func (e *YamlEncoderImpl) WriteString(v string)   { e.writeScalar(encodeString(v)) }
func (e *YamlEncoderImpl) WriteInt(v int)         { e.writeScalar(strconv.Itoa(v)) }
func (e *YamlEncoderImpl) WriteInt64(v int64)     { e.writeScalar(strconv.FormatInt(v, 10)) }
func (e *YamlEncoderImpl) WriteUint64(v uint64)   { e.writeScalar(strconv.FormatUint(v, 10)) }
func (e *YamlEncoderImpl) WriteFloat64(v float64) { e.writeScalar(formatYamlFloat(v, 64)) }
func (e *YamlEncoderImpl) WriteFloat32(v float32) { e.writeScalar(formatYamlFloat(float64(v), 32)) }

// formatYamlFloat renders a float in the shortest form that parses back to
// the same value at the given bit size. NaN and the infinities use YAML's
// core-schema spellings (.nan, .inf, -.inf), which parseYamlFloat accepts.
func formatYamlFloat(v float64, bitSize int) string {
	switch {
	case math.IsNaN(v):
		return ".nan"
	case math.IsInf(v, 1):
		return ".inf"
	case math.IsInf(v, -1):
		return "-.inf"
	}
	return strconv.FormatFloat(v, 'f', -1, bitSize)
}
func (e *YamlEncoderImpl) WriteBool(v bool) {
	if v {
		e.writeScalar("true")
	} else {
		e.writeScalar("false")
	}
}
func (e *YamlEncoderImpl) WriteRune(v rune) { e.writeScalar(encodeString(string(v))) }
func (e *YamlEncoderImpl) WriteNull()       { e.writeScalar("null") }

// plainOrQuoted picks plain-form for keys / safe values, quoted form
// otherwise. Keys go through this; scalar values use encodeString (which
// also quotes reserved literals like "true" or "42" to preserve type).
func plainOrQuoted(s string) string {
	if isPlainSafe(s) {
		return s
	}
	return doubleQuoted(s)
}

func encodeString(s string) string {
	if !isPlainSafe(s) || looksReserved(s) {
		return doubleQuoted(s)
	}
	return s
}

func isPlainSafe(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == ' ' || s[0] == '\t' {
		return false
	}
	// A trailing ':' would read back as "key:" — a mapping, not a string.
	if s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == ':' {
		return false
	}
	switch s[0] {
	case '-', '?', ':', ',', '[', ']', '{', '}', '#', '&', '*', '!',
		'|', '>', '\'', '"', '%', '@', '`':
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' || c == '\r' || c == '\t' || c < 32 {
			return false
		}
		// ": " mid-string would be parsed as a nested mapping.
		if c == ':' && i+1 < len(s) && (s[i+1] == ' ' || s[i+1] == '\t') {
			return false
		}
		// " #" starts a comment.
		if c == ' ' && i+1 < len(s) && s[i+1] == '#' {
			return false
		}
	}
	return true
}

func looksReserved(s string) bool {
	switch s {
	case "true", "false", "null", "~",
		"yes", "no", "on", "off",
		"True", "False", "Null", "TRUE", "FALSE", "NULL":
		return true
	}
	if _, ok := yamlSpecialFloat(s); ok {
		return true
	}
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return true
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return true
	}
	return false
}

func doubleQuoted(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			sb.WriteString("\\\"")
		case '\\':
			sb.WriteString("\\\\")
		case '\n':
			sb.WriteString("\\n")
		case '\r':
			sb.WriteString("\\r")
		case '\t':
			sb.WriteString("\\t")
		default:
			if c < 32 {
				sb.WriteString(fmt.Sprintf("\\x%02x", c))
			} else {
				sb.WriteByte(c)
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// Compile-time check: YamlEncoderImpl must satisfy std.FieldEncoder.
var _ std.FieldEncoder = (*YamlEncoderImpl)(nil)

// toRunes / runesToString are package-private helpers used by naming.gala
// (the GALA parser doesn't accept []rune(s) / string(rs) conversions).
// Mirror of json/byte_utils.go's same-named helpers.
func toRunes(s string) []rune        { return []rune(s) }
func runesToString(rs []rune) string { return string(rs) }

// ============================================================================
// YamlDecoderImpl — implements std.FieldDecoder.
//
// Strategy: parse the whole document into a tiny in-memory AST first, then
// have the FieldDecoder walk it via a position cursor. This keeps the
// indent-driven parser separate from the structural pull API and makes both
// pieces independently testable.
// ============================================================================

type yKind byte

const (
	yScalar yKind = 's'
	yMap    yKind = 'm'
	ySeq    yKind = 'l'
)

type yNode struct {
	kind    yKind
	scalar  string
	nullish bool     // true for null / empty / ~
	mapKeys []string // parallel to mapVals, ordered
	mapVals []*yNode
	seq     []*yNode
}

func newScalarNode(s string, isNull bool) *yNode {
	return &yNode{kind: yScalar, scalar: s, nullish: isNull}
}

type yFrame struct {
	node *yNode
	idx  int  // next child to read
	keyR bool // mapping: ReadKey already consumed for the current pair
}

type YamlDecoderImpl struct {
	root  *yNode
	stack []*yFrame
}

func NewYamlDecoder(input string) *YamlDecoderImpl {
	return &YamlDecoderImpl{root: parseYAML(input)}
}

// peekChild returns the next-to-be-consumed child. For mappings, that's the
// value paired with the most recent ReadKey. For sequences, the next element.
func (d *YamlDecoderImpl) peekChild() *yNode {
	if len(d.stack) == 0 {
		return d.root
	}
	top := d.stack[len(d.stack)-1]
	if top.node.kind == ySeq {
		return top.node.seq[top.idx]
	}
	return top.node.mapVals[top.idx-1] // mapping: value sits at idx-1 after ReadKey
}

func (d *YamlDecoderImpl) advanceSeq() {
	d.stack[len(d.stack)-1].idx++
}

func (d *YamlDecoderImpl) StartObject() {
	if len(d.stack) == 0 {
		if d.root == nil {
			d.root = &yNode{kind: yMap}
		}
		if d.root.kind != yMap {
			panic("yaml: expected mapping at root")
		}
		d.stack = append(d.stack, &yFrame{node: d.root})
		return
	}
	n := d.peekChild()
	if n == nil || (n.kind != yMap && !(n.kind == yScalar && n.nullish)) {
		panic(fmt.Sprintf("yaml: expected mapping, got %s", n.kindName()))
	}
	if n.kind == yScalar {
		// Empty/null value where caller expects an object — empty mapping.
		d.stack = append(d.stack, &yFrame{node: &yNode{kind: yMap}})
		return
	}
	d.stack = append(d.stack, &yFrame{node: n})
}

func (d *YamlDecoderImpl) EndObject() {
	if len(d.stack) == 0 || d.stack[len(d.stack)-1].node.kind != yMap {
		panic("yaml: EndObject without matching StartObject")
	}
	d.stack = d.stack[:len(d.stack)-1]
	d.maybeAdvanceParentSeq()
}

func (d *YamlDecoderImpl) StartArray() {
	if len(d.stack) == 0 {
		if d.root == nil || d.root.kind != ySeq {
			panic("yaml: expected sequence at root")
		}
		d.stack = append(d.stack, &yFrame{node: d.root})
		return
	}
	n := d.peekChild()
	if n == nil || (n.kind != ySeq && !(n.kind == yScalar && n.nullish)) {
		panic(fmt.Sprintf("yaml: expected sequence, got %s", n.kindName()))
	}
	if n.kind == yScalar {
		d.stack = append(d.stack, &yFrame{node: &yNode{kind: ySeq}})
		return
	}
	d.stack = append(d.stack, &yFrame{node: n})
}

func (d *YamlDecoderImpl) EndArray() {
	if len(d.stack) == 0 || d.stack[len(d.stack)-1].node.kind != ySeq {
		panic("yaml: EndArray without matching StartArray")
	}
	d.stack = d.stack[:len(d.stack)-1]
	d.maybeAdvanceParentSeq()
}

// maybeAdvanceParentSeq advances the parent sequence's index after closing a
// child container. Mapping parents don't need this because ReadKey already
// bumped idx and the value is consumed by closing the inner container.
func (d *YamlDecoderImpl) maybeAdvanceParentSeq() {
	if len(d.stack) == 0 {
		return
	}
	top := d.stack[len(d.stack)-1]
	if top.node.kind == ySeq {
		top.idx++
	}
}

func (d *YamlDecoderImpl) HasMoreFields() bool {
	if len(d.stack) == 0 {
		return false
	}
	top := d.stack[len(d.stack)-1]
	return top.node.kind == yMap && top.idx < len(top.node.mapKeys)
}

func (d *YamlDecoderImpl) HasMoreElements() bool {
	if len(d.stack) == 0 {
		return false
	}
	top := d.stack[len(d.stack)-1]
	return top.node.kind == ySeq && top.idx < len(top.node.seq)
}

func (d *YamlDecoderImpl) ReadKey() string {
	top := d.stack[len(d.stack)-1]
	key := top.node.mapKeys[top.idx]
	top.idx++
	top.keyR = true
	return key
}

// readScalarText returns the raw scalar text for the next-to-consume value.
// Mappings: the value paired with the most recent ReadKey. Sequences: the
// next element (and advances).
func (d *YamlDecoderImpl) readScalarText() (string, bool) {
	if len(d.stack) == 0 {
		if d.root == nil || d.root.kind != yScalar {
			panic("yaml: expected scalar at root")
		}
		return d.root.scalar, d.root.nullish
	}
	n := d.peekChild()
	if n == nil {
		panic("yaml: missing value")
	}
	if n.kind != yScalar {
		panic(fmt.Sprintf("yaml: expected scalar, got %s", n.kindName()))
	}
	if d.stack[len(d.stack)-1].node.kind == ySeq {
		d.advanceSeq()
	}
	return n.scalar, n.nullish
}

func (d *YamlDecoderImpl) ReadString() string {
	s, _ := d.readScalarText()
	return s
}

func (d *YamlDecoderImpl) ReadInt() int {
	s, _ := d.readScalarText()
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		panic(fmt.Sprintf("yaml: invalid int %q: %v", s, err))
	}
	return v
}

func (d *YamlDecoderImpl) ReadInt64() int64 {
	s, _ := d.readScalarText()
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		panic(fmt.Sprintf("yaml: invalid int64 %q: %v", s, err))
	}
	return v
}

// ReadIntN reads a signed integer that must fit in bitSize bits; a value
// outside that range is an error, never a silent wrap.
func (d *YamlDecoderImpl) ReadIntN(bitSize int) int64 {
	s, _ := d.readScalarText()
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, bitSize)
	if err != nil {
		panic(fmt.Sprintf("yaml: invalid %s %q: %v", intKindName("int", bitSize), s, err))
	}
	return v
}

// ReadUintN reads an unsigned integer that must fit in bitSize bits;
// negative or out-of-range values are errors.
func (d *YamlDecoderImpl) ReadUintN(bitSize int) uint64 {
	s, _ := d.readScalarText()
	v, err := strconv.ParseUint(strings.TrimSpace(s), 10, bitSize)
	if err != nil {
		panic(fmt.Sprintf("yaml: invalid %s %q: %v", intKindName("uint", bitSize), s, err))
	}
	return v
}

// intKindName names the Go integer kind a bit size stands for; 0 is the
// platform-sized int/uint, as in strconv.
func intKindName(prefix string, bitSize int) string {
	if bitSize == 0 {
		return prefix
	}
	return prefix + strconv.Itoa(bitSize)
}

func (d *YamlDecoderImpl) ReadFloat64() float64 {
	s, _ := d.readScalarText()
	return parseYamlFloat(s, 64)
}

func (d *YamlDecoderImpl) ReadFloat32() float32 {
	s, _ := d.readScalarText()
	return float32(parseYamlFloat(s, 32))
}

// parseYamlFloat parses a float at the given bit size. The YAML core-schema
// spellings of NaN and the infinities are accepted; a finite literal that
// overflows the bit size is an error rather than a silent infinity.
func parseYamlFloat(raw string, bitSize int) float64 {
	s := strings.TrimSpace(raw)
	if v, ok := yamlSpecialFloat(s); ok {
		return v
	}
	v, err := strconv.ParseFloat(s, bitSize)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		panic(fmt.Sprintf("yaml: invalid float%d %q", bitSize, raw))
	}
	return v
}

// yamlSpecialFloat recognises the YAML 1.2 core-schema spellings of NaN and
// the infinities, and Go's NaN / +Inf / -Inf, which earlier releases of this
// encoder wrote, so documents they produced still decode.
func yamlSpecialFloat(s string) (float64, bool) {
	switch s {
	case ".nan", ".NaN", ".NAN", "NaN":
		return math.NaN(), true
	case ".inf", ".Inf", ".INF", "+.inf", "+.Inf", "+.INF", "+Inf":
		return math.Inf(1), true
	case "-.inf", "-.Inf", "-.INF", "-Inf":
		return math.Inf(-1), true
	}
	return 0, false
}

func (d *YamlDecoderImpl) ReadBool() bool {
	s, _ := d.readScalarText()
	switch strings.TrimSpace(s) {
	case "true", "True", "TRUE", "yes", "on":
		return true
	case "false", "False", "FALSE", "no", "off":
		return false
	}
	panic(fmt.Sprintf("yaml: invalid bool %q", s))
}

func (d *YamlDecoderImpl) ReadRune() rune {
	s := d.ReadString()
	rs := []rune(s)
	if len(rs) == 0 {
		return rune(0)
	}
	return rs[0]
}

func (d *YamlDecoderImpl) IsNull() bool {
	if len(d.stack) == 0 {
		return d.root == nil || d.root.nullish
	}
	n := d.peekChild()
	return n == nil || (n.kind == yScalar && n.nullish)
}

func (d *YamlDecoderImpl) ReadNull() {
	if !d.IsNull() {
		panic("yaml: expected null")
	}
	if len(d.stack) == 0 {
		// A null document root — `null`, or an empty document — is the
		// whole value; there is nothing to step past.
		return
	}
	_, _ = d.readScalarText()
}

func (d *YamlDecoderImpl) Skip() {
	if len(d.stack) == 0 {
		return
	}
	if d.stack[len(d.stack)-1].node.kind == ySeq {
		d.advanceSeq()
	}
}

func (n *yNode) kindName() string {
	if n == nil {
		return "nil"
	}
	switch n.kind {
	case yScalar:
		return "scalar"
	case yMap:
		return "mapping"
	case ySeq:
		return "sequence"
	}
	return "?"
}

// Compile-time check: YamlDecoderImpl must satisfy std.FieldDecoder.
var _ std.FieldDecoder = (*YamlDecoderImpl)(nil)

// ============================================================================
// YAML parser — builds the AST from raw text.
//
// Subset (per DESIGN.md §4.5): block-style mappings, block-style sequences,
// plain / single-quoted / double-quoted scalars, literal block scalars (`|`),
// comments, blank lines. Out of scope: anchors, aliases, flow style, custom
// tags, folded blocks (`>`), explicit document markers (`---`).
// ============================================================================

type pLine struct {
	indent  int
	raw     string
	lineNum int
}

func parseYAML(input string) *yNode {
	lines := splitSignificantLines(input)
	if len(lines) == 0 {
		// An empty document is null, and reads as an empty mapping where a
		// struct is expected.
		return &yNode{kind: yMap, nullish: true}
	}
	// A one-line document that is neither a sequence item nor a "key: value"
	// pair is a bare scalar ("42", "abc", null) or an empty flow container
	// ("[]", "{}") — the forms the encoder writes for a non-struct root.
	if len(lines) == 1 && !isSequenceLine(lines[0].raw) {
		if _, _, isPair := tryParseKeyValue(lines[0].raw); !isPair {
			return parseInlineScalar(withoutComment(lines[0].raw))
		}
	}
	node, pos := parseBlock(lines, 0, lines[0].indent)
	if pos != len(lines) {
		panic(fmt.Sprintf("yaml: unexpected content at line %d", lines[pos].lineNum))
	}
	return node
}

func splitSignificantLines(input string) []pLine {
	rawLines := strings.Split(input, "\n")
	var out []pLine
	for i := 0; i < len(rawLines); i++ {
		raw := strings.TrimRight(rawLines[i], " \t\r")
		if isBlankOrComment(raw) {
			continue
		}
		indent := countLeadingSpaces(raw)
		content := raw[indent:]
		if endsWithLiteralBlock(content) {
			joined, consumed := takeLiteralBlock(rawLines, i+1, indent)
			collapsed := stripLiteralBlockMarker(content) + " " + doubleQuoted(joined)
			out = append(out, pLine{indent: indent, raw: collapsed, lineNum: i + 1})
			i += consumed
			continue
		}
		out = append(out, pLine{indent: indent, raw: content, lineNum: i + 1})
	}
	return out
}

func isBlankOrComment(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t':
			continue
		case '#':
			return true
		}
		return false
	}
	return true
}

func countLeadingSpaces(s string) int {
	n := 0
	for n < len(s) && s[n] == ' ' {
		n++
	}
	return n
}

func endsWithLiteralBlock(content string) bool {
	t := strings.TrimRight(content, " \t")
	if t == "|" || t == "|-" {
		return true
	}
	return strings.HasSuffix(t, ": |") || strings.HasSuffix(t, ": |-")
}

func stripLiteralBlockMarker(content string) string {
	t := strings.TrimRight(content, " \t")
	if strings.HasSuffix(t, " |-") {
		return t[:len(t)-3]
	}
	if strings.HasSuffix(t, " |") {
		return t[:len(t)-2]
	}
	if t == "|" || t == "|-" {
		return ""
	}
	return t
}

func takeLiteralBlock(raw []string, start, parentIndent int) (string, int) {
	var body []string
	consumed := 0
	bodyIndent := -1
	for i := start; i < len(raw); i++ {
		line := strings.TrimRight(raw[i], "\r")
		if line == "" {
			if bodyIndent >= 0 {
				body = append(body, "")
			}
			consumed++
			continue
		}
		indent := countLeadingSpaces(line)
		if indent <= parentIndent {
			break
		}
		if bodyIndent < 0 {
			bodyIndent = indent
		}
		if indent < bodyIndent {
			break
		}
		body = append(body, line[bodyIndent:])
		consumed++
	}
	return strings.Join(body, "\n"), consumed
}

func parseBlock(lines []pLine, start, expectedIndent int) (*yNode, int) {
	if start >= len(lines) {
		return newScalarNode("", true), start
	}
	first := lines[start]
	if first.indent != expectedIndent {
		panic(fmt.Sprintf("yaml: indent mismatch at line %d (got %d, want %d)",
			first.lineNum, first.indent, expectedIndent))
	}
	if isSequenceLine(first.raw) {
		return parseSequence(lines, start, expectedIndent)
	}
	return parseMapping(lines, start, expectedIndent)
}

func isSequenceLine(content string) bool {
	if content == "-" {
		return true
	}
	return len(content) >= 2 && content[0] == '-' && content[1] == ' '
}

func parseMapping(lines []pLine, start, indent int) (*yNode, int) {
	m := &yNode{kind: yMap}
	i := start
	for i < len(lines) {
		line := lines[i]
		if line.indent < indent {
			break
		}
		if line.indent > indent {
			panic(fmt.Sprintf("yaml: unexpected indent at line %d (got %d, want %d)",
				line.lineNum, line.indent, indent))
		}
		if isSequenceLine(line.raw) {
			break
		}
		key, valueOnLine, hasValue := splitKeyValue(line.raw, line.lineNum)
		m.mapKeys = append(m.mapKeys, key)
		if hasValue {
			m.mapVals = append(m.mapVals, parseInlineScalar(valueOnLine))
			i++
			continue
		}
		// No inline value — child block on subsequent indented lines.
		if i+1 >= len(lines) || lines[i+1].indent <= indent {
			m.mapVals = append(m.mapVals, newScalarNode("", true))
			i++
			continue
		}
		child, np := parseBlock(lines, i+1, lines[i+1].indent)
		m.mapVals = append(m.mapVals, child)
		i = np
	}
	return m, i
}

func parseSequence(lines []pLine, start, indent int) (*yNode, int) {
	l := &yNode{kind: ySeq}
	i := start
	for i < len(lines) {
		line := lines[i]
		if line.indent < indent {
			break
		}
		if line.indent > indent {
			panic(fmt.Sprintf("yaml: unexpected indent at line %d", line.lineNum))
		}
		if !isSequenceLine(line.raw) {
			break
		}
		itemPart := withoutComment(strings.TrimSpace(line.raw[1:]))
		if itemPart == "" {
			// Bare "-" — value lives on subsequent indented lines.
			if i+1 >= len(lines) || lines[i+1].indent <= indent {
				l.seq = append(l.seq, newScalarNode("", true))
				i++
				continue
			}
			child, np := parseBlock(lines, i+1, lines[i+1].indent)
			l.seq = append(l.seq, child)
			i = np
			continue
		}
		if isCompactMappingItem(itemPart) {
			child, np := parseCompactMapping(lines, i, indent, itemPart)
			l.seq = append(l.seq, child)
			i = np
			continue
		}
		l.seq = append(l.seq, parseInlineScalar(itemPart))
		i++
	}
	return l, i
}

func isCompactMappingItem(s string) bool {
	_, _, ok := tryParseKeyValue(s)
	return ok
}

func parseCompactMapping(lines []pLine, start, parentIndent int, firstItem string) (*yNode, int) {
	m := &yNode{kind: yMap}
	k, vRaw, _ := tryParseKeyValue(firstItem)
	m.mapKeys = append(m.mapKeys, k)
	itemKeyIndent := parentIndent + 2
	if vRaw == "" {
		// "- key:" with no inline value — value lives on indented lines below.
		if start+1 < len(lines) && lines[start+1].indent > itemKeyIndent {
			child, np := parseBlock(lines, start+1, lines[start+1].indent)
			m.mapVals = append(m.mapVals, child)
			return m, np
		}
		m.mapVals = append(m.mapVals, newScalarNode("", true))
		return m, start + 1
	}
	m.mapVals = append(m.mapVals, parseInlineScalar(vRaw))
	// Continuation lines aligned with the first key belong to the same item.
	i := start + 1
	for i < len(lines) {
		line := lines[i]
		if line.indent < itemKeyIndent {
			break
		}
		if line.indent > itemKeyIndent {
			panic(fmt.Sprintf("yaml: unexpected indent at line %d", line.lineNum))
		}
		if isSequenceLine(line.raw) {
			break
		}
		k2, v2, has2 := splitKeyValue(line.raw, line.lineNum)
		m.mapKeys = append(m.mapKeys, k2)
		if has2 {
			m.mapVals = append(m.mapVals, parseInlineScalar(v2))
			i++
			continue
		}
		if i+1 >= len(lines) || lines[i+1].indent <= itemKeyIndent {
			m.mapVals = append(m.mapVals, newScalarNode("", true))
			i++
			continue
		}
		child, np := parseBlock(lines, i+1, lines[i+1].indent)
		m.mapVals = append(m.mapVals, child)
		i = np
	}
	return m, i
}

func splitKeyValue(line string, lineNum int) (string, string, bool) {
	k, v, ok := tryParseKeyValue(line)
	if !ok {
		panic(fmt.Sprintf("yaml: expected 'key:' at line %d, got %q", lineNum, line))
	}
	return k, v, v != ""
}

func tryParseKeyValue(line string) (string, string, bool) {
	if len(line) > 0 && (line[0] == '"' || line[0] == '\'') {
		return parseQuotedKey(line)
	}
	ci := findUnquotedColon(line)
	if ci < 0 {
		return "", "", false
	}
	key := strings.TrimSpace(line[:ci])
	value := ""
	if ci+1 < len(line) {
		value = withoutComment(strings.TrimSpace(line[ci+1:]))
	}
	return key, value, true
}

// withoutComment drops a trailing comment (" # ...", outside quotes) from a
// scalar's text.
func withoutComment(s string) string {
	if hi := findUnquotedHash(s); hi >= 0 {
		return strings.TrimSpace(s[:hi])
	}
	return s
}

// findUnquotedColon returns the index of the first ':' followed by whitespace
// (or end-of-line) that is not inside a quoted region; -1 if none.
func findUnquotedColon(s string) int {
	inSingle, inDouble := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && inDouble && i+1 < len(s) {
			i++
			continue
		}
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == ':' && !inSingle && !inDouble:
			if i+1 == len(s) {
				return i
			}
			if next := s[i+1]; next == ' ' || next == '\t' {
				return i
			}
		}
	}
	return -1
}

func findUnquotedHash(s string) int {
	inSingle, inDouble := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && inDouble && i+1 < len(s) {
			i++
			continue
		}
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == '#' && !inSingle && !inDouble:
			if i == 0 || s[i-1] == ' ' || s[i-1] == '\t' {
				return i
			}
		}
	}
	return -1
}

func parseQuotedKey(line string) (string, string, bool) {
	quote := line[0]
	for i := 1; i < len(line); i++ {
		c := line[i]
		if quote == '"' && c == '\\' {
			i++ // an escaped character never closes the key
			continue
		}
		if c != quote {
			continue
		}
		j := i + 1
		for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
			j++
		}
		if j >= len(line) || line[j] != ':' {
			return "", "", false
		}
		rest := ""
		if j+1 < len(line) {
			rest = strings.TrimSpace(line[j+1:])
		}
		key := line[1:i]
		if quote == '"' {
			key = unescapeDoubleQuoted(key)
		}
		return key, rest, true
	}
	return "", "", false
}

func parseInlineScalar(raw string) *yNode {
	s := strings.TrimSpace(raw)
	if s == "" || s == "~" || s == "null" || s == "Null" || s == "NULL" {
		return newScalarNode("", true)
	}
	// Flow-style empty containers, which the encoder writes for an empty
	// Array/List/HashMap/struct.
	switch s {
	case "[]":
		return &yNode{kind: ySeq}
	case "{}":
		return &yNode{kind: yMap}
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return newScalarNode(unescapeDoubleQuoted(s[1:len(s)-1]), false)
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return newScalarNode(decodeSingleQuoted(s), false)
	}
	return newScalarNode(s, false)
}

// yamlCharEscapes maps the one-character escapes of a YAML double-quoted
// scalar to the character each one stands for (YAML 1.2, section 5.7).
var yamlCharEscapes = map[byte]rune{
	'0':  0x00,
	'a':  0x07,
	'b':  0x08,
	't':  0x09,
	'\t': 0x09,
	'n':  0x0a,
	'v':  0x0b,
	'f':  0x0c,
	'r':  0x0d,
	'e':  0x1b,
	' ':  ' ',
	'"':  '"',
	'/':  '/',
	'\\': '\\',
	'N':  0x85,
	'_':  0xa0,
	'L':  0x2028,
	'P':  0x2029,
}

// yamlCodePointEscapes maps the code-point escapes to their number of hex
// digits: \xXX, \uXXXX and \UXXXXXXXX.
var yamlCodePointEscapes = map[byte]int{'x': 2, 'u': 4, 'U': 8}

// unescapeDoubleQuoted decodes the body of a YAML double-quoted scalar (the
// text between the quotes). A code-point escape names a Unicode character and
// is written out as UTF-8, so `\xe9` and `é` both decode to "é".
//
// The parser reports no errors, so an escape that cannot be decoded (an
// unknown letter, too few or invalid hex digits, a surrogate or out-of-range
// code point) is kept as written rather than silently dropped.
func unescapeDoubleQuoted(body string) string {
	if strings.IndexByte(body, '\\') < 0 {
		return body
	}
	var sb strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' || i+1 >= len(body) {
			sb.WriteByte(c)
			continue
		}
		esc := body[i+1]
		if r, ok := yamlCharEscapes[esc]; ok {
			sb.WriteRune(r)
			i++
			continue
		}
		if digits, ok := yamlCodePointEscapes[esc]; ok && i+2+digits <= len(body) {
			v, err := strconv.ParseUint(body[i+2:i+2+digits], 16, 4*digits)
			if err == nil && v <= utf8.MaxRune && utf8.ValidRune(rune(v)) {
				sb.WriteRune(rune(v))
				i += 1 + digits
				continue
			}
		}
		sb.WriteByte(c) // not decodable: keep the backslash and what follows
	}
	return sb.String()
}

func decodeSingleQuoted(s string) string {
	var sb strings.Builder
	end := len(s) - 1
	for i := 1; i < end; i++ {
		c := s[i]
		if c == '\'' && i+1 < end && s[i+1] == '\'' {
			sb.WriteByte('\'')
			i++
			continue
		}
		sb.WriteByte(c)
	}
	return sb.String()
}
