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
	"strconv"
	"strings"

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
	buf           bytes.Buffer
	depth         int    // current indent level (each level = 2 spaces)
	stack         []byte // frame kinds: 'r' root, 'o' mapping, 'a' sequence
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
func (e *YamlEncoderImpl) startContainerPrelude(isObject bool) {
	parent := e.topFrame()
	if parent == 'r' && !e.startedDoc {
		// Document root — no prelude, depth stays 0.
		e.startedDoc = true
		return
	}
	if e.hasKey {
		// We're a value of a mapping pair: emit "key:\n", then indent children.
		e.emitKeyHeader(e.pendingKey)
		e.buf.WriteByte('\n')
		e.depth++
		e.pendingKey = ""
		e.hasKey = false
		return
	}
	if parent == 'a' {
		// We're a list element that is itself a container.
		e.writeIndent()
		if isObject {
			// Compact form: "- " then the first key inline.
			e.buf.WriteString("- ")
			e.depth++
			e.inlineNextKey = true
		} else {
			// Nested sequence in sequence: bare "-" line, indent inner.
			e.buf.WriteString("-\n")
			e.depth++
		}
		return
	}
	panic("yaml encoder: container started with no key in mapping context")
}

func (e *YamlEncoderImpl) WriteStartObject() {
	e.startContainerPrelude(true)
	e.stack = append(e.stack, 'o')
}

func (e *YamlEncoderImpl) WriteEndObject() {
	if len(e.stack) == 0 || e.stack[len(e.stack)-1] != 'o' {
		panic("yaml encoder: WriteEndObject without matching StartObject")
	}
	e.stack = e.stack[:len(e.stack)-1]
	if e.topFrame() != 'r' && e.depth > 0 {
		e.depth--
	}
	e.inlineNextKey = false
}

func (e *YamlEncoderImpl) WriteStartArray() {
	e.startContainerPrelude(false)
	e.stack = append(e.stack, 'a')
}

func (e *YamlEncoderImpl) WriteEndArray() {
	if len(e.stack) == 0 || e.stack[len(e.stack)-1] != 'a' {
		panic("yaml encoder: WriteEndArray without matching StartArray")
	}
	e.stack = e.stack[:len(e.stack)-1]
	if e.topFrame() != 'r' && e.depth > 0 {
		e.depth--
	}
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
func (e *YamlEncoderImpl) WriteFloat64(v float64) { e.writeScalar(strconv.FormatFloat(v, 'f', -1, 64)) }
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
	if s[len(s)-1] == ' ' || s[len(s)-1] == '\t' {
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
func toRunes(s string) []rune       { return []rune(s) }
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
	nullish bool         // true for null / empty / ~
	mapKeys []string     // parallel to mapVals, ordered
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

func (d *YamlDecoderImpl) ReadFloat64() float64 {
	s, _ := d.readScalarText()
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		panic(fmt.Sprintf("yaml: invalid float %q: %v", s, err))
	}
	return v
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
		return d.root == nil || (d.root.kind == yScalar && d.root.nullish)
	}
	n := d.peekChild()
	return n == nil || (n.kind == yScalar && n.nullish)
}

func (d *YamlDecoderImpl) ReadNull() {
	if !d.IsNull() {
		panic("yaml: expected null")
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
		return &yNode{kind: yMap}
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
		itemPart := strings.TrimSpace(line.raw[1:])
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
		value = strings.TrimSpace(line[ci+1:])
		if hi := findUnquotedHash(value); hi >= 0 {
			value = strings.TrimSpace(value[:hi])
		}
	}
	return key, value, true
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
	var sb strings.Builder
	for i := 1; i < len(line); i++ {
		c := line[i]
		if quote == '"' && c == '\\' && i+1 < len(line) {
			esc := line[i+1]
			switch esc {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case '"':
				sb.WriteByte('"')
			case '\\':
				sb.WriteByte('\\')
			default:
				sb.WriteByte(esc)
			}
			i++
			continue
		}
		if c == quote {
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
			return sb.String(), rest, true
		}
		sb.WriteByte(c)
	}
	return "", "", false
}

func parseInlineScalar(raw string) *yNode {
	s := strings.TrimSpace(raw)
	if s == "" || s == "~" || s == "null" || s == "Null" || s == "NULL" {
		return newScalarNode("", true)
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return newScalarNode(decodeDoubleQuoted(s), false)
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return newScalarNode(decodeSingleQuoted(s), false)
	}
	return newScalarNode(s, false)
}

func decodeDoubleQuoted(s string) string {
	var sb strings.Builder
	end := len(s) - 1
	for i := 1; i < end; i++ {
		c := s[i]
		if c == '\\' && i+1 < end {
			esc := s[i+1]
			switch esc {
			case 'n':
				sb.WriteByte('\n')
				i++
				continue
			case 't':
				sb.WriteByte('\t')
				i++
				continue
			case 'r':
				sb.WriteByte('\r')
				i++
				continue
			case '"':
				sb.WriteByte('"')
				i++
				continue
			case '\\':
				sb.WriteByte('\\')
				i++
				continue
			case '/':
				sb.WriteByte('/')
				i++
				continue
			case 'x':
				if i+3 < end {
					if v, err := strconv.ParseUint(s[i+2:i+4], 16, 32); err == nil {
						sb.WriteByte(byte(v))
					}
					i += 3
					continue
				}
			default:
				sb.WriteByte(esc)
				i++
				continue
			}
		}
		sb.WriteByte(c)
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
