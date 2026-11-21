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
	"unicode/utf16"
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
// the same value at the given bit size: plain notation for
// 1e-6 <= |v| < 1e21 and exponent notation outside it, so 1e300 is not a
// 301-digit literal. The range is the one encoding/json uses (json's
// formatJsonFloat keeps the same rule in GALA). The exponent form always has
// a '.' in the mantissa and a signed exponent (1.0e+21, 1.5e-9), which YAML
// 1.1 resolvers such as PyYAML require for a float and YAML 1.2 accepts. NaN
// and the infinities use YAML's core-schema spellings (.nan, .inf, -.inf),
// which parseYamlFloat accepts.
func formatYamlFloat(v float64, bitSize int) string {
	switch {
	case math.IsNaN(v):
		return ".nan"
	case math.IsInf(v, 1):
		return ".inf"
	case math.IsInf(v, -1):
		return "-.inf"
	}
	if !useExponent(math.Abs(v), bitSize) {
		return strconv.FormatFloat(v, 'f', -1, bitSize)
	}
	s := strconv.FormatFloat(v, 'e', -1, bitSize)
	mantissa, exponent, _ := strings.Cut(s, "e")
	if !strings.Contains(mantissa, ".") {
		mantissa += ".0"
	}
	// A positive exponent here is at least 21, so only a one-digit negative
	// one carries a leading zero to drop (e-7, not e-07).
	return mantissa + "e" + strings.Replace(exponent, "-0", "-", 1)
}

// useExponent reports whether abs lies outside the plain-notation range,
// tested at the value's own precision: float32(1e-6) is just below the
// float64 1e-6, yet it is written plainly.
func useExponent(abs float64, bitSize int) bool {
	if abs == 0 {
		return false
	}
	if bitSize == 32 {
		a := float32(abs)
		return a < 1e-6 || a >= 1e21
	}
	return abs < 1e-6 || abs >= 1e21
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
	if s == "" || !utf8.ValidString(s) {
		return false
	}
	if s[0] == ' ' {
		return false
	}
	// A trailing ':' would read back as "key:" — a mapping, not a string.
	if s[len(s)-1] == ' ' || s[len(s)-1] == ':' {
		return false
	}
	switch s[0] {
	case '-', '?', ':', ',', '[', ']', '{', '}', '#', '&', '*', '!',
		'|', '>', '\'', '"', '%', '@', '`':
		return false
	}
	// Tabs, line breaks and the other characters that need an escape can
	// only be written in a double-quoted scalar.
	if strings.IndexFunc(s, needsYamlEscape) >= 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		// ": " mid-string would be parsed as a nested mapping.
		if c == ':' && i+1 < len(s) && s[i+1] == ' ' {
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
			// A YAML stream is Unicode text of printable characters: a
			// printable character is copied through, any other is escaped,
			// and an invalid byte is written as the U+FFFD escape.
			r, size := rune(c), 1
			if c >= utf8.RuneSelf {
				r, size = utf8.DecodeRuneInString(s[i:])
			}
			switch {
			case r == utf8.RuneError && size == 1:
				sb.WriteString(`\ufffd`)
			case r == 0x85:
				sb.WriteString(`\N`)
			case r == 0x2028:
				sb.WriteString(`\L`)
			case r == 0x2029:
				sb.WriteString(`\P`)
			case needsYamlEscape(r) && r <= 0xff:
				fmt.Fprintf(&sb, `\x%02x`, r)
			case needsYamlEscape(r): // U+FEFF, U+FFFE, U+FFFF
				fmt.Fprintf(&sb, `\u%04X`, r)
			default:
				sb.WriteString(s[i : i+size])
			}
			i += size - 1
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// needsYamlEscape reports whether r is written as an escape. YAML 1.2
// (section 5.1) does not allow the C0 controls, DEL, the C1 controls, the
// byte order mark or the noncharacters U+FFFE and U+FFFF as is; tab and line
// breaks are C0 controls a plain scalar cannot hold either. NEL and the line
// and paragraph separators U+2028 and U+2029 are line breaks to YAML 1.1
// readers such as libyaml and gopkg.in/yaml.v3, so they are escaped too.
func needsYamlEscape(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == 0x2028 || r == 0x2029 ||
		r == 0xfeff || r == 0xfffe || r == 0xffff
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
	// A YAML stream is Unicode text. Each invalid UTF-8 byte reads as U+FFFD,
	// as in the json decoder, whatever the style of the scalar it sits in.
	if !utf8.ValidString(input) {
		input = string([]rune(input))
	}
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
	nested  int // sequence items opened on this line before raw ("- - x" re-read as "- x" has 1)
}

// maxItemsPerLine caps the sequence items a single line may open ("- - - x"
// opens three). Each one is a level of recursion in the parser.
const maxItemsPerLine = 1000

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
	// A final line break ends the last line; it does not start another one,
	// which a literal block kept with "|+" would count as a blank line.
	// Without it the last line has no break, which a block ending there
	// must not gain.
	lastHasBreak := strings.HasSuffix(input, "\n")
	rawLines := strings.Split(strings.TrimSuffix(input, "\n"), "\n")
	var out []pLine
	for i := 0; i < len(rawLines); i++ {
		raw := strings.TrimRight(rawLines[i], " \t\r")
		if isBlankOrComment(raw) {
			continue
		}
		indent := countLeadingSpaces(raw)
		content := raw[indent:]
		if before, parent, indicator, ok := literalBlockHeader(content, indent); ok {
			joined, consumed := takeLiteralBlock(rawLines, i+1, parent, indicator, lastHasBreak)
			out = append(out, pLine{indent: indent, raw: before + doubleQuoted(joined), lineNum: i + 1})
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

// literalBlockHeader reports whether content, a line without its
// indentation, opens a literal block scalar: a bare "|", a sequence item
// "- |" (or "- - |"), or a pair "key: |" (also as an item, "- key: |"),
// each optionally "|-" or "|+" and followed by a comment. It returns the
// text that stands before the block's body once the body is collapsed into
// a double-quoted scalar, the column the body must be indented past, and
// the indicator, which says how the body's final line breaks are chomped.
//
// The pair is split by tryParseKeyValue, so a "#" or ": |" inside a quoted
// or flow value, or inside a quoted key, is never taken for a comment or a
// block indicator. Only a line holding a '|' can open a block, so only such
// a line pays for the split.
func literalBlockHeader(content string, indent int) (string, int, string, bool) {
	if strings.IndexByte(content, '|') < 0 {
		return "", 0, "", false
	}
	n, lastDash := itemPrefix(content)
	rest := content[n:]
	if indicator := withoutComment(rest); isBlockIndicator(indicator) {
		// A bare "|" leaves no text before the body, so the collapsed line
		// starts with the quote and reads as a quoted scalar, never a key.
		if n == 0 {
			return "", indent, indicator, true
		}
		return content[:n], indent + lastDash, indicator, true
	}
	key, value, isPair := tryParseKeyValue(rest)
	if !isPair || !isBlockIndicator(value) || !spaceAfterQuotedKey(rest) {
		return "", 0, "", false
	}
	// The key is written back double-quoted, which reads back as the same
	// key whatever it holds.
	return content[:n] + doubleQuoted(key) + ": ", indent + n, value, true
}

func isBlockIndicator(s string) bool { return s == "|" || s == "|-" || s == "|+" }

// spaceAfterQuotedKey reports whether, in a pair whose key is quoted, white
// space separates the colon from the value, as a block indicator needs: in
// "k":| the "|" is plain text. A plain key's colon is always followed by
// white space, so any other pair passes.
func spaceAfterQuotedKey(pair string) bool {
	if pair[0] != '"' && pair[0] != '\'' {
		return true
	}
	j := closingQuote(pair) + 1
	for j < len(pair) && (pair[j] == ' ' || pair[j] == '\t') {
		j++
	}
	// tryParseKeyValue found the pair, so pair[j] is the colon.
	return j+1 < len(pair) && (pair[j+1] == ' ' || pair[j+1] == '\t')
}

// itemPrefix measures the "- " markers a line starts with, one per sequence
// item opened on that line ("- - key: |" has two). It returns the length of
// all of them and the offset of the last dash. Only a dash with text after
// it counts, so a bare "-" (alone, or last as in "- -") is not counted, and
// a line with no such dash gives 0, 0.
func itemPrefix(s string) (n, lastDash int) {
	for len(s) > n+1 && isSequenceLine(s[n:]) {
		lastDash = n
		n += 1 + countLeadingSpaces(s[n+1:])
	}
	return n, lastDash
}

// takeLiteralBlock reads the body of a literal block scalar from raw[start:]
// and returns its text with the chomping that indicator ("|", "|-" or "|+")
// asks for, and the number of lines it consumed. The body is the lines
// indented past parentIndent, at the indentation of its first line, with
// blank lines among them. YAML 1.2 (section 8.1.1.2) chomps the line breaks
// at the end: "|" (clip) keeps one, "|-" (strip) keeps none and "|+" (keep)
// keeps them all. Blank lines before the first text line are kept.
// lastHasBreak says whether the document's last line ends in a line break.
func takeLiteralBlock(raw []string, start, parentIndent int, indicator string, lastHasBreak bool) (string, int) {
	var body []string
	consumed := 0
	bodyIndent := -1
	blanks := 0 // blank lines since the last text line
	for i := start; i < len(raw); i++ {
		line := strings.TrimRight(raw[i], "\r")
		if line == "" {
			blanks++
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
		for ; blanks > 0; blanks-- {
			body = append(body, "")
		}
		body = append(body, line[bodyIndent:])
		consumed++
	}
	text := strings.Join(body, "\n")
	// The line breaks after the text: the last text line's own, then one
	// per blank line after it, less one when the block's last line ends the
	// document without a break (YAML 1.2, rule 165).
	breaks := blanks
	if len(body) > 0 {
		breaks++
	}
	if breaks > 0 && start+consumed == len(raw) && !lastHasBreak {
		breaks--
	}
	switch {
	case indicator == "|-":
		return text, consumed
	case indicator == "|+":
		return text + strings.Repeat("\n", breaks), consumed
	case len(body) == 0 || breaks == 0:
		return text, consumed
	default:
		return text + "\n", consumed
	}
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
	return m, addMappingPairs(m, lines, start, indent)
}

// addMappingPairs adds to m the "key: value" pairs whose keys start at
// column indent, from lines[start] on, and returns the index of the first
// line that is not part of them.
func addMappingPairs(m *yNode, lines []pLine, start, indent int) int {
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
		key, valueOnLine := splitKeyValue(line.raw, line.lineNum)
		child, np := parseMappingValue(lines, i, indent, valueOnLine)
		m.mapKeys = append(m.mapKeys, key)
		m.mapVals = append(m.mapVals, child)
		i = np
	}
	return i
}

// parseMappingValue parses the value of the mapping key on lines[i], whose
// key starts at column keyIndent, and returns it with the index of the line
// after it. The value is the text after the colon, when there is any, or
// else the block on the lines below: lines indented deeper than the key, or
// a block sequence at the key's own indentation. YAML allows a sequence that
// is a mapping value to sit there, since the "- " counts as indentation, and
// kubectl and many CI files write it that way:
//
//	containers:
//	- name: app
func parseMappingValue(lines []pLine, i, keyIndent int, value string) (*yNode, int) {
	if value != "" {
		return parseInlineScalar(value), i + 1
	}
	if i+1 < len(lines) {
		next := lines[i+1]
		if next.indent > keyIndent {
			return parseBlock(lines, i+1, next.indent)
		}
		if next.indent == keyIndent && isSequenceLine(next.raw) {
			return parseSequence(lines, i+1, keyIndent)
		}
	}
	return newScalarNode("", true), i + 1
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
		// The item's content starts after the dash and the spaces that follow
		// it: a nested sequence's items, or a mapping's keys, line up there.
		contentIndent := indent + 1 + countLeadingSpaces(line.raw[1:])
		if rest := line.raw[contentIndent-indent:]; isSequenceLine(rest) {
			// A nested sequence that starts on the dash's line ("- - x").
			// This line is rewritten in place as that sequence's first item
			// and parsed again; the parser reads each line once otherwise.
			// This check comes before any scan of the rest of the line, so a
			// line of many nested dashes still parses in linear time, and
			// their number is capped: each one is a level of recursion, and
			// running out of stack would end the process, not fail the decode.
			if line.nested+2 > maxItemsPerLine { // the items this line holds after opening one more
				panic(fmt.Sprintf("yaml: more than %d nested sequence items on line %d", maxItemsPerLine, line.lineNum))
			}
			lines[i] = pLine{indent: contentIndent, raw: rest, lineNum: line.lineNum, nested: line.nested + 1}
			child, np := parseSequence(lines, i, contentIndent)
			l.seq = append(l.seq, child)
			i = np
			continue
		}
		itemPart := trimYamlSpace(line.raw[1:])
		// A "- key: value" item drops its comment while the pair is split,
		// where a quoted value is known; any other item drops it here.
		if key, value, ok := tryParseKeyValue(itemPart); ok {
			child, np := parseCompactMapping(lines, i, contentIndent, key, value)
			l.seq = append(l.seq, child)
			i = np
			continue
		}
		itemPart = withoutComment(itemPart)
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
		l.seq = append(l.seq, parseInlineScalar(itemPart))
		i++
	}
	return l, i
}

// parseCompactMapping parses the mapping a sequence item on lines[start]
// starts on the dash's line ("- key: value"), whose first key starts at
// column keyIndent. Its first pair is key and value; the keys on the lines
// below, aligned with the first one, belong to the same item, whatever
// form the first key's value took.
func parseCompactMapping(lines []pLine, start, keyIndent int, key, value string) (*yNode, int) {
	first, i := parseMappingValue(lines, start, keyIndent, value)
	m := &yNode{kind: yMap, mapKeys: []string{key}, mapVals: []*yNode{first}}
	return m, addMappingPairs(m, lines, i, keyIndent)
}

func splitKeyValue(line string, lineNum int) (string, string) {
	k, v, ok := tryParseKeyValue(line)
	if !ok {
		panic(fmt.Sprintf("yaml: expected 'key:' at line %d, got %q", lineNum, line))
	}
	return k, v
}

func tryParseKeyValue(line string) (string, string, bool) {
	if len(line) > 0 && (line[0] == '"' || line[0] == '\'') {
		return parseQuotedKey(line)
	}
	ci := findKeyColon(line)
	if ci < 0 {
		return "", "", false
	}
	key := trimYamlSpace(line[:ci])
	value := ""
	if ci+1 < len(line) {
		value = withoutComment(trimYamlSpace(line[ci+1:]))
	}
	return key, value, true
}

// trimYamlSpace trims the white space around a scalar's text. YAML white
// space is the space and the tab only (YAML 1.2, section 5.5): a no-break
// space or another Unicode space at either end is part of the text.
func trimYamlSpace(s string) string { return strings.Trim(s, " \t") }

// withoutComment drops a trailing comment (" # ...", outside a quoted
// scalar) from a scalar's text.
func withoutComment(s string) string {
	if hi := findComment(s, 0); hi >= 0 {
		return trimYamlSpace(s[:hi])
	}
	return s
}

// findKeyColon returns the index of the first ':' followed by white space
// (or the end of the line) in a line whose key is a plain scalar; -1 if
// there is none before a comment. A plain scalar has no quoted regions: a
// quote inside it, as in it's, is text.
func findKeyColon(s string) int {
	return scanPlain(s, 0, true)
}

// findComment returns the index of the '#' that starts a comment in the
// value text s, at index from or later; -1 if there is none. A '#' starts
// a comment at the start of s or after white space. When the value opens
// with a quote it is a quoted scalar, and a comment can only follow its
// closing quote; a quote anywhere else is text.
func findComment(s string, from int) int {
	t := 0
	for t < len(s) && (s[t] == ' ' || s[t] == '\t') {
		t++
	}
	if t < len(s) && (s[t] == '"' || s[t] == '\'') {
		end := closingQuote(s[t:])
		if end < 0 {
			return -1 // unterminated: the scalar parser reports it
		}
		from = max(from, t+end+1)
	}
	return scanPlain(s, from, false)
}

// scanPlain returns, from index from on, the index of the '#' that starts a
// comment, or with wantColon the index of the first ':' followed by white
// space (or the end) before any comment; -1 if there is none. In plain text
// a quote is text. Flow-style text ("[a, \"b # c\"]", "{...}"), which this
// parser keeps as a string, is different: a quote inside it opens a quoted
// item that may hold "#" or ": ", so quotes are tracked there.
func scanPlain(s string, from int, wantColon bool) int {
	// Whether s is flow text depends on its first character, not on where
	// the scan starts.
	t := 0
	for t < len(s) && (s[t] == ' ' || s[t] == '\t') {
		t++
	}
	flow := t < len(s) && (s[t] == '[' || s[t] == '{')
	quote := byte(0)
	for i := from; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == '"' && c == '\\':
			i++ // an escaped character never closes the quote
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case flow && (c == '"' || c == '\''):
			quote = c
		case c == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t'):
			if wantColon {
				return -1 // the rest is a comment
			}
			return i
		case wantColon && c == ':' && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\t'):
			return i
		}
	}
	return -1
}

// parseQuotedKey splits a line whose key is a quoted scalar into the key
// and the value text after the colon; false when the line is not
// `"key": ...` or `'key': ...`.
func parseQuotedKey(line string) (string, string, bool) {
	i := closingQuote(line)
	if i < 0 {
		return "", "", false
	}
	j := i + 1
	for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
		j++
	}
	if j >= len(line) || line[j] != ':' {
		return "", "", false
	}
	rest := line[j+1:]
	// Drop a trailing comment, as for a plain key's value. A '#' right
	// after the colon, as in "k":#x, is text: a comment needs white space
	// before it.
	from := 0
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		from = 1
	}
	if hi := findComment(rest, from); hi >= 0 {
		rest = rest[:hi]
	}
	rest = trimYamlSpace(rest)
	if line[0] == '"' {
		return unescapeDoubleQuoted(line[1:i]), rest, true
	}
	return unescapeSingleQuoted(line[1:i]), rest, true
}

func parseInlineScalar(raw string) *yNode {
	s := trimYamlSpace(raw)
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
	// A scalar that starts with a quote must be one quoted scalar: an
	// unterminated quote, or text after the closing one, is an error rather
	// than a plain scalar with a stray quote.
	if q := s[0]; q == '"' || q == '\'' {
		// The common case, checked here to keep it cheap: the first quote
		// after the opening one ends s and is not escaped, so it closes the
		// scalar. Single-quoted text then holds no '' to decode.
		end := strings.IndexByte(s[1:], q) + 1
		if end > 0 && end == len(s)-1 {
			if q == '\'' {
				return newScalarNode(s[1:end], false)
			}
			if s[end-1] != '\\' {
				return newScalarNode(unescapeDoubleQuoted(s[1:end]), false)
			}
		}
		return newScalarNode(decodeQuotedScalar(s), false)
	}
	return newScalarNode(s, false)
}

// decodeQuotedScalar decodes the quoted scalar s, or fails when s is not one.
func decodeQuotedScalar(s string) string {
	switch end := closingQuote(s); {
	case end < 0:
		panic(fmt.Sprintf("yaml: unterminated quoted scalar %q", s))
	case end != len(s)-1:
		panic(fmt.Sprintf("yaml: unexpected text after quoted scalar %q", s))
	case s[0] == '"':
		return unescapeDoubleQuoted(s[1:end])
	default:
		return unescapeSingleQuoted(s[1:end])
	}
}

// unescapeSingleQuoted decodes the body of a well-formed single-quoted
// scalar, where a quote is always written twice and stands for one.
func unescapeSingleQuoted(body string) string {
	var sb strings.Builder
	sb.Grow(len(body))
	for i := 0; i < len(body); i++ {
		sb.WriteByte(body[i])
		if body[i] == '\'' {
			i++
		}
	}
	return sb.String()
}

// closingQuote returns the index of the quote that closes the quoted scalar
// s starts with, or -1 if none does. In a double-quoted scalar a backslash
// escapes the next character, so a '"' after an odd run of backslashes is
// escaped; in a single-quoted one a quote written twice stands for one.
func closingQuote(s string) int {
	quote := s[0]
	for i := 1; i < len(s); {
		j := strings.IndexByte(s[i:], quote) + i
		switch {
		case j < i:
			return -1
		case quote == '"':
			k := j
			for s[k-1] == '\\' { // s[0] is the opening quote, so k stays > 0
				k--
			}
			if (j-k)%2 == 0 {
				return j
			}
		case j+1 == len(s) || s[j+1] != '\'':
			return j
		default:
			j++ // '' stands for one quote
		}
		i = j + 1
	}
	return -1
}

// yamlCharEscape returns the character a one-character escape of a YAML
// double-quoted scalar stands for (YAML 1.2, section 5.7). A switch rather
// than a map keeps the lookup off the hash path.
func yamlCharEscape(esc byte) (rune, bool) {
	switch esc {
	case '0':
		return 0x00, true
	case 'a':
		return 0x07, true
	case 'b':
		return 0x08, true
	case 't', '\t':
		return 0x09, true
	case 'n':
		return 0x0a, true
	case 'v':
		return 0x0b, true
	case 'f':
		return 0x0c, true
	case 'r':
		return 0x0d, true
	case 'e':
		return 0x1b, true
	case ' ', '"', '/', '\\':
		return rune(esc), true
	case 'N':
		return 0x85, true
	case '_':
		return 0xa0, true
	case 'L':
		return 0x2028, true
	case 'P':
		return 0x2029, true
	}
	return 0, false
}

// yamlCodePointEscapes maps the code-point escapes to their number of hex
// digits: \xXX, \uXXXX and \UXXXXXXXX.
var yamlCodePointEscapes = map[byte]int{'x': 2, 'u': 4, 'U': 8}

// unescapeDoubleQuoted decodes the body of a YAML double-quoted scalar (the
// text between the quotes). A code-point escape names a Unicode character and
// is written out as UTF-8, so `\xe9` and `é` both decode to "é".
//
// A \u escape of a high surrogate followed by a \u escape of a low one is
// JSON's spelling of a character above U+FFFF, and decodes to it.
//
// An escape YAML does not define (an unknown letter, too few or invalid hex
// digits, an unpaired surrogate or an out-of-range code point) fails the
// decode, as YAML parsers do, rather than reaching the value as written.
func unescapeDoubleQuoted(body string) string {
	if strings.IndexByte(body, '\\') < 0 {
		return body
	}
	var sb strings.Builder
	sb.Grow(len(body))
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' {
			sb.WriteByte(c)
			continue
		}
		if i+1 == len(body) {
			// The callers find the closing quote first, so a body never
			// ends in a lone backslash; fail cleanly if one ever does.
			invalidEscape(body[i:])
		}
		esc := body[i+1]
		if r, ok := yamlCharEscape(esc); ok {
			sb.WriteRune(r)
			i++
			continue
		}
		digits, ok := yamlCodePointEscapes[esc]
		if !ok {
			_, size := utf8.DecodeRuneInString(body[i+1:])
			invalidEscape(body[i : i+1+size])
		}
		end := i + 2 + digits
		r, ok := hexRune(body, i+2, end)
		if ok && esc == 'u' && utf16.IsSurrogate(r) && strings.HasPrefix(body[end:], `\u`) {
			if low, lowOK := hexRune(body, end+2, end+6); lowOK {
				if pair := utf16.DecodeRune(r, low); pair != utf8.RuneError {
					r, end = pair, end+6
				}
			}
		}
		if !ok || !utf8.ValidRune(r) {
			// Show the escape: its letter and up to as many characters as it
			// takes digits, stopping at white space or the next backslash.
			end = i + 2
			for n := 0; n < digits && end < len(body) && !strings.ContainsRune(" \t\\", rune(body[end])); n++ {
				_, size := utf8.DecodeRuneInString(body[end:])
				end += size
			}
			invalidEscape(body[i:end])
		}
		sb.WriteRune(r)
		i = end - 1
	}
	return sb.String()
}

// hexRune reads the code point written in hex as body[from:to]; false when
// body is too short, the digits are not hex or the value is above U+10FFFF.
func hexRune(body string, from, to int) (rune, bool) {
	if to > len(body) {
		return 0, false
	}
	v, err := strconv.ParseUint(body[from:to], 16, 32)
	if err != nil || v > utf8.MaxRune {
		return 0, false
	}
	return rune(v), true
}

// invalidEscape fails the decode for the escape esc, shown as written when
// it is printable and quoted when it is not.
func invalidEscape(esc string) {
	if !utf8.ValidString(esc) || strings.IndexFunc(esc, func(r rune) bool { return !strconv.IsPrint(r) }) >= 0 {
		esc = strconv.Quote(esc)
	}
	panic("yaml: invalid escape " + esc + " in double-quoted scalar")
}
