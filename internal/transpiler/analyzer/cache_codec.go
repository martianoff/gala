package analyzer

// Hand-rolled binary codec for CachedRichAST.
//
// Why this exists: gob.Decode is the dominant per-cache-hit cost on CI
// runners. Profiling on the apex transpile of cmd/main.gala in gala_team
// (~26 cached packages, the largest at 417 KB) showed gob spending most of
// its time in reflection — 43k allocations per decode of the largest entry.
// Replacing it with a tagged-union binary format keeps the on-disk shape
// straight (no schema generator, no third-party deps) and drops decode to
// a fraction of the gob baseline by skipping reflection entirely.
//
// Format: a fixed magic+version header, then each field of CachedRichAST
// is emitted in declaration order. Strings are length-prefixed. Maps and
// slices are length-prefixed. Pointer fields are nil-prefixed (1 byte:
// 0=nil, 1=present). The Type interface is encoded with a 1-byte tag
// identifying the concrete variant; nil interface values use tag 0.
//
// Backward compatibility: the cache directory key embeds CacheVersion,
// which is bumped from "v1" to "v2" alongside this codec change.
// Pre-v2 caches live under the old version-keyed directory and are
// pruned by the existing stale-cache GC.
import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"martianoff/gala/internal/transpiler"
)

// codecMagic identifies a binary cache blob. The trailing byte is the
// format version; bump alongside CacheVersion when the layout changes.
var codecMagic = [4]byte{'G', 'A', 'C', 0x02}

const (
	typeTagNil     uint8 = 0 // nil interface
	typeTagBasic   uint8 = 1
	typeTagNamed   uint8 = 2
	typeTagGeneric uint8 = 3
	typeTagArray   uint8 = 4
	typeTagMap     uint8 = 5
	typeTagPointer uint8 = 6
	typeTagFunc    uint8 = 7
	typeTagNilType uint8 = 8 // concrete NilType{}
	typeTagVoid    uint8 = 9
)

// encodeCachedRichAST serializes c using the binary codec. The returned
// byte slice can be persisted directly; decodeCachedRichAST is its
// inverse.
func encodeCachedRichAST(c *CachedRichAST) ([]byte, error) {
	if c == nil {
		return nil, errors.New("cache codec: nil CachedRichAST")
	}
	// Pre-allocate; ~realistic packages weigh in around the size of the
	// gob output, so 256 KiB is a reasonable starting capacity.
	e := &encoder{buf: make([]byte, 0, 256*1024)}
	e.buf = append(e.buf, codecMagic[:]...)
	e.writeString(c.PackageName)
	e.writeStringTypeMetaMap(c.Types)
	e.writeStringFuncMetaMap(c.Functions)
	e.writeStringStringMap(c.Packages)
	e.writeStringCompanionMap(c.CompanionObjects)
	e.writeGoExports(c.GoExports)
	e.writeGoTypeInfoPtr(c.GoTypeInfo)
	e.writeStringTypeMap(c.TypeAliases)
	e.writeStringStringMap(c.ImportPathMap)
	e.writeString(c.DepsHash)
	e.writeStringSlice(c.DirectImports)
	if e.err != nil {
		return nil, e.err
	}
	return e.buf, nil
}

// decodeCachedRichAST is the inverse of encodeCachedRichAST. It returns
// an error if the input does not begin with the expected magic+version
// header or if any length prefix is inconsistent with the buffer size.
func decodeCachedRichAST(data []byte) (*CachedRichAST, error) {
	if len(data) < len(codecMagic) {
		return nil, fmt.Errorf("cache codec: input too short (%d bytes)", len(data))
	}
	if data[0] != codecMagic[0] || data[1] != codecMagic[1] || data[2] != codecMagic[2] {
		return nil, errors.New("cache codec: bad magic")
	}
	if data[3] != codecMagic[3] {
		return nil, fmt.Errorf("cache codec: unsupported version %d", data[3])
	}
	d := &decoder{buf: data[len(codecMagic):]}
	out := &CachedRichAST{}
	out.PackageName = d.readString()
	out.Types = d.readStringTypeMetaMap()
	out.Functions = d.readStringFuncMetaMap()
	out.Packages = d.readStringStringMap()
	out.CompanionObjects = d.readStringCompanionMap()
	out.GoExports = d.readGoExports()
	out.GoTypeInfo = d.readGoTypeInfoPtr()
	out.TypeAliases = d.readStringTypeMap()
	out.ImportPathMap = d.readStringStringMap()
	out.DepsHash = d.readString()
	out.DirectImports = d.readStringSlice()
	if d.err != nil {
		return nil, d.err
	}
	return out, nil
}

// -------- encoder --------

type encoder struct {
	buf []byte
	err error
}

func (e *encoder) writeUvarint(v uint64) {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	e.buf = append(e.buf, tmp[:n]...)
}

func (e *encoder) writeByte(b byte) { e.buf = append(e.buf, b) }

func (e *encoder) writeBool(b bool) {
	if b {
		e.buf = append(e.buf, 1)
	} else {
		e.buf = append(e.buf, 0)
	}
}

func (e *encoder) writeInt32(v int32) {
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], uint32(v))
	e.buf = append(e.buf, tmp[:]...)
}

func (e *encoder) writeString(s string) {
	e.writeUvarint(uint64(len(s)))
	e.buf = append(e.buf, s...)
}

func (e *encoder) writeStringSlice(s []string) {
	e.writeUvarint(uint64(len(s)))
	for _, v := range s {
		e.writeString(v)
	}
}

func (e *encoder) writeBoolSlice(s []bool) {
	e.writeUvarint(uint64(len(s)))
	for _, v := range s {
		e.writeBool(v)
	}
}

func (e *encoder) writeStringStringMap(m map[string]string) {
	e.writeUvarint(uint64(len(m)))
	for k, v := range m {
		e.writeString(k)
		e.writeString(v)
	}
}

func (e *encoder) writeSourcePos(p transpiler.SourcePos) {
	e.writeInt32(int32(p.Line))
	e.writeInt32(int32(p.Column))
}

func (e *encoder) writeStringSourcePosMap(m map[string]transpiler.SourcePos) {
	e.writeUvarint(uint64(len(m)))
	for k, v := range m {
		e.writeString(k)
		e.writeSourcePos(v)
	}
}

// writeType emits a tagged Type. Recursive Type values are emitted via
// the same routine so nested generic shapes (e.g. Future[Either[A, B]])
// round-trip transparently.
func (e *encoder) writeType(t transpiler.Type) {
	if t == nil {
		e.writeByte(typeTagNil)
		return
	}
	switch v := t.(type) {
	case transpiler.BasicType:
		e.writeByte(typeTagBasic)
		e.writeString(v.Name)
	case transpiler.NamedType:
		e.writeByte(typeTagNamed)
		e.writeString(v.Package)
		e.writeString(v.Name)
		e.writeString(v.ImportPath)
	case transpiler.GenericType:
		e.writeByte(typeTagGeneric)
		e.writeType(v.Base)
		e.writeUvarint(uint64(len(v.Params)))
		for _, p := range v.Params {
			e.writeType(p)
		}
	case transpiler.ArrayType:
		e.writeByte(typeTagArray)
		e.writeType(v.Elem)
	case transpiler.MapType:
		e.writeByte(typeTagMap)
		e.writeType(v.Key)
		e.writeType(v.Elem)
	case transpiler.PointerType:
		e.writeByte(typeTagPointer)
		e.writeType(v.Elem)
	case transpiler.FuncType:
		e.writeByte(typeTagFunc)
		e.writeUvarint(uint64(len(v.Params)))
		for _, p := range v.Params {
			e.writeType(p)
		}
		e.writeUvarint(uint64(len(v.Results)))
		for _, p := range v.Results {
			e.writeType(p)
		}
	case transpiler.NilType:
		e.writeByte(typeTagNilType)
	case transpiler.VoidType:
		e.writeByte(typeTagVoid)
	default:
		if e.err == nil {
			e.err = fmt.Errorf("cache codec: unknown Type variant %T", t)
		}
	}
}

func (e *encoder) writeTypeSlice(s []transpiler.Type) {
	e.writeUvarint(uint64(len(s)))
	for _, t := range s {
		e.writeType(t)
	}
}

func (e *encoder) writeStringTypeMap(m map[string]transpiler.Type) {
	e.writeUvarint(uint64(len(m)))
	for k, v := range m {
		e.writeString(k)
		e.writeType(v)
	}
}

func (e *encoder) writeMethodMeta(m *transpiler.MethodMetadata) {
	if m == nil {
		e.writeBool(false)
		return
	}
	e.writeBool(true)
	e.writeString(m.Name)
	e.writeString(m.Package)
	e.writeSourcePos(m.Pos)
	e.writeTypeSlice(m.ParamTypes)
	e.writeStringSlice(m.ParamNames)
	e.writeType(m.ReturnType)
	e.writeStringSlice(m.TypeParams)
	// DefaultExprs is map[int]string — emit count, then (idx,string) pairs.
	e.writeUvarint(uint64(len(m.DefaultExprs)))
	for idx, expr := range m.DefaultExprs {
		e.writeInt32(int32(idx))
		e.writeString(expr)
	}
	e.writeString(m.ReceiverName)
	e.writeBool(m.IsGeneric)
	e.writeString(m.DefinedIn)
}

// writeStringMethodMap emits a map[string]*MethodMetadata.
func (e *encoder) writeStringMethodMap(m map[string]*transpiler.MethodMetadata) {
	e.writeUvarint(uint64(len(m)))
	for k, v := range m {
		e.writeString(k)
		e.writeMethodMeta(v)
	}
}

func (e *encoder) writeSealedVariant(v transpiler.SealedVariant) {
	e.writeString(v.Name)
	e.writeSourcePos(v.Pos)
	e.writeStringSlice(v.FieldNames)
	e.writeTypeSlice(v.FieldTypes)
}

func (e *encoder) writeSealedVariantSlice(s []transpiler.SealedVariant) {
	e.writeUvarint(uint64(len(s)))
	for _, v := range s {
		e.writeSealedVariant(v)
	}
}

func (e *encoder) writeTypeMeta(t *transpiler.TypeMetadata) {
	if t == nil {
		e.writeBool(false)
		return
	}
	e.writeBool(true)
	e.writeString(t.Name)
	e.writeString(t.Package)
	e.writeSourcePos(t.Pos)
	e.writeStringMethodMap(t.Methods)
	e.writeStringTypeMap(t.Fields)
	e.writeStringSlice(t.FieldNames)
	e.writeStringSourcePosMap(t.FieldPositions)
	e.writeStringSlice(t.TypeParams)
	e.writeStringStringMap(t.TypeParamConstraints)
	e.writeBoolSlice(t.ImmutFlags)
	e.writeBool(t.IsSealed)
	e.writeSealedVariantSlice(t.SealedVariants)
	e.writeString(t.DefinedIn)
}

func (e *encoder) writeStringTypeMetaMap(m map[string]*transpiler.TypeMetadata) {
	e.writeUvarint(uint64(len(m)))
	for k, v := range m {
		e.writeString(k)
		e.writeTypeMeta(v)
	}
}

func (e *encoder) writeFuncMeta(f *transpiler.FunctionMetadata) {
	if f == nil {
		e.writeBool(false)
		return
	}
	e.writeBool(true)
	e.writeString(f.Name)
	e.writeString(f.Package)
	e.writeSourcePos(f.Pos)
	e.writeTypeSlice(f.ParamTypes)
	e.writeStringSlice(f.ParamNames)
	e.writeBoolSlice(f.ParamImmutFlags)
	e.writeType(f.ReturnType)
	e.writeStringSlice(f.TypeParams)
	e.writeUvarint(uint64(len(f.DefaultExprs)))
	for idx, expr := range f.DefaultExprs {
		e.writeInt32(int32(idx))
		e.writeString(expr)
	}
	e.writeString(f.DefinedIn)
}

func (e *encoder) writeStringFuncMetaMap(m map[string]*transpiler.FunctionMetadata) {
	e.writeUvarint(uint64(len(m)))
	for k, v := range m {
		e.writeString(k)
		e.writeFuncMeta(v)
	}
}

func (e *encoder) writeCompanion(c *transpiler.CompanionObjectMetadata) {
	if c == nil {
		e.writeBool(false)
		return
	}
	e.writeBool(true)
	e.writeString(c.Name)
	e.writeString(c.Package)
	e.writeString(c.TargetType)
	e.writeUvarint(uint64(len(c.ExtractIndices)))
	for _, v := range c.ExtractIndices {
		e.writeInt32(int32(v))
	}
}

func (e *encoder) writeStringCompanionMap(m map[string]*transpiler.CompanionObjectMetadata) {
	e.writeUvarint(uint64(len(m)))
	for k, v := range m {
		e.writeString(k)
		e.writeCompanion(v)
	}
}

func (e *encoder) writeGoExports(m map[string][]string) {
	e.writeUvarint(uint64(len(m)))
	for k, v := range m {
		e.writeString(k)
		e.writeStringSlice(v)
	}
}

func (e *encoder) writeGoParam(p transpiler.GoParam) {
	e.writeString(p.Name)
	e.writeType(p.Type)
}

func (e *encoder) writeGoFuncSig(s *transpiler.GoFuncSignature) {
	if s == nil {
		e.writeBool(false)
		return
	}
	e.writeBool(true)
	e.writeUvarint(uint64(len(s.Params)))
	for _, p := range s.Params {
		e.writeGoParam(p)
	}
	e.writeTypeSlice(s.Returns)
	e.writeBool(s.IsVariadic)
	e.writeStringSlice(s.TypeParams)
}

func (e *encoder) writeStringGoFuncSigMap(m map[string]*transpiler.GoFuncSignature) {
	e.writeUvarint(uint64(len(m)))
	for k, v := range m {
		e.writeString(k)
		e.writeGoFuncSig(v)
	}
}

func (e *encoder) writeGoTypeData(t *transpiler.GoTypeData) {
	if t == nil {
		e.writeBool(false)
		return
	}
	e.writeBool(true)
	e.writeString(t.Kind)
	e.writeStringTypeMap(t.Fields)
	e.writeStringSlice(t.FieldOrder)
	e.writeStringGoFuncSigMap(t.Methods)
	e.writeType(t.Underlying)
	e.writeStringSlice(t.TypeParams)
}

func (e *encoder) writeStringGoTypeDataMap(m map[string]*transpiler.GoTypeData) {
	e.writeUvarint(uint64(len(m)))
	for k, v := range m {
		e.writeString(k)
		e.writeGoTypeData(v)
	}
}

func (e *encoder) writeGoTypeInfoPtr(g *transpiler.GoTypeInfo) {
	if g == nil {
		e.writeBool(false)
		return
	}
	e.writeBool(true)
	e.writeStringGoFuncSigMap(g.Functions)
	e.writeStringGoTypeDataMap(g.Types)
	e.writeStringTypeMap(g.Variables)
	e.writeStringTypeMap(g.Constants)
	e.writeStringTypeMap(g.TypeAliases)
}

// -------- decoder --------

type decoder struct {
	buf []byte
	err error
}

func (d *decoder) fail(format string, args ...any) {
	if d.err == nil {
		d.err = fmt.Errorf("cache codec: "+format, args...)
	}
}

func (d *decoder) need(n int) bool {
	if d.err != nil {
		return false
	}
	if len(d.buf) < n {
		d.err = io.ErrUnexpectedEOF
		return false
	}
	return true
}

func (d *decoder) readByte() byte {
	if !d.need(1) {
		return 0
	}
	b := d.buf[0]
	d.buf = d.buf[1:]
	return b
}

func (d *decoder) readBool() bool {
	return d.readByte() == 1
}

func (d *decoder) readInt32() int32 {
	if !d.need(4) {
		return 0
	}
	v := binary.LittleEndian.Uint32(d.buf[:4])
	d.buf = d.buf[4:]
	return int32(v)
}

func (d *decoder) readUvarint() uint64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Uvarint(d.buf)
	if n <= 0 {
		d.fail("bad uvarint")
		return 0
	}
	d.buf = d.buf[n:]
	return v
}

func (d *decoder) readString() string {
	n := d.readUvarint()
	if d.err != nil {
		return ""
	}
	if !d.need(int(n)) {
		return ""
	}
	// Convert from buf bytes — copying via string() is safe and gives us
	// an independent allocation (the buf slice is held by the decoder
	// only for the duration of the call, but the caller may keep the
	// returned string alive much longer).
	s := string(d.buf[:n])
	d.buf = d.buf[n:]
	return s
}

func (d *decoder) readStringSlice() []string {
	n := d.readUvarint()
	if d.err != nil || n == 0 {
		return nil
	}
	out := make([]string, n)
	for i := range out {
		out[i] = d.readString()
	}
	return out
}

func (d *decoder) readBoolSlice() []bool {
	n := d.readUvarint()
	if d.err != nil || n == 0 {
		return nil
	}
	out := make([]bool, n)
	for i := range out {
		out[i] = d.readBool()
	}
	return out
}

func (d *decoder) readStringStringMap() map[string]string {
	n := d.readUvarint()
	if d.err != nil || n == 0 {
		return nil
	}
	out := make(map[string]string, n)
	for i := uint64(0); i < n; i++ {
		k := d.readString()
		v := d.readString()
		out[k] = v
	}
	return out
}

func (d *decoder) readSourcePos() transpiler.SourcePos {
	line := int(d.readInt32())
	col := int(d.readInt32())
	return transpiler.SourcePos{Line: line, Column: col}
}

func (d *decoder) readStringSourcePosMap() map[string]transpiler.SourcePos {
	n := d.readUvarint()
	if d.err != nil || n == 0 {
		return nil
	}
	out := make(map[string]transpiler.SourcePos, n)
	for i := uint64(0); i < n; i++ {
		k := d.readString()
		v := d.readSourcePos()
		out[k] = v
	}
	return out
}

func (d *decoder) readType() transpiler.Type {
	if d.err != nil {
		return nil
	}
	tag := d.readByte()
	switch tag {
	case typeTagNil:
		return nil
	case typeTagBasic:
		return transpiler.BasicType{Name: d.readString()}
	case typeTagNamed:
		pkg := d.readString()
		name := d.readString()
		ip := d.readString()
		return transpiler.NamedType{Package: pkg, Name: name, ImportPath: ip}
	case typeTagGeneric:
		base := d.readType()
		n := d.readUvarint()
		var params []transpiler.Type
		if n > 0 {
			params = make([]transpiler.Type, n)
			for i := range params {
				params[i] = d.readType()
			}
		}
		return transpiler.GenericType{Base: base, Params: params}
	case typeTagArray:
		return transpiler.ArrayType{Elem: d.readType()}
	case typeTagMap:
		k := d.readType()
		v := d.readType()
		return transpiler.MapType{Key: k, Elem: v}
	case typeTagPointer:
		return transpiler.PointerType{Elem: d.readType()}
	case typeTagFunc:
		np := d.readUvarint()
		var params []transpiler.Type
		if np > 0 {
			params = make([]transpiler.Type, np)
			for i := range params {
				params[i] = d.readType()
			}
		}
		nr := d.readUvarint()
		var results []transpiler.Type
		if nr > 0 {
			results = make([]transpiler.Type, nr)
			for i := range results {
				results[i] = d.readType()
			}
		}
		return transpiler.FuncType{Params: params, Results: results}
	case typeTagNilType:
		return transpiler.NilType{}
	case typeTagVoid:
		return transpiler.VoidType{}
	default:
		d.fail("unknown type tag %d", tag)
		return nil
	}
}

func (d *decoder) readTypeSlice() []transpiler.Type {
	n := d.readUvarint()
	if d.err != nil || n == 0 {
		return nil
	}
	out := make([]transpiler.Type, n)
	for i := range out {
		out[i] = d.readType()
	}
	return out
}

func (d *decoder) readStringTypeMap() map[string]transpiler.Type {
	n := d.readUvarint()
	if d.err != nil || n == 0 {
		return nil
	}
	out := make(map[string]transpiler.Type, n)
	for i := uint64(0); i < n; i++ {
		k := d.readString()
		v := d.readType()
		out[k] = v
	}
	return out
}

func (d *decoder) readMethodMeta() *transpiler.MethodMetadata {
	if !d.readBool() {
		return nil
	}
	m := &transpiler.MethodMetadata{}
	m.Name = d.readString()
	m.Package = d.readString()
	m.Pos = d.readSourcePos()
	m.ParamTypes = d.readTypeSlice()
	m.ParamNames = d.readStringSlice()
	m.ReturnType = d.readType()
	m.TypeParams = d.readStringSlice()
	// DefaultExprs.
	dn := d.readUvarint()
	if dn > 0 {
		m.DefaultExprs = make(map[int]string, dn)
		for i := uint64(0); i < dn; i++ {
			idx := int(d.readInt32())
			m.DefaultExprs[idx] = d.readString()
		}
	}
	m.ReceiverName = d.readString()
	m.IsGeneric = d.readBool()
	m.DefinedIn = d.readString()
	return m
}

func (d *decoder) readStringMethodMap() map[string]*transpiler.MethodMetadata {
	n := d.readUvarint()
	if d.err != nil || n == 0 {
		return nil
	}
	out := make(map[string]*transpiler.MethodMetadata, n)
	for i := uint64(0); i < n; i++ {
		k := d.readString()
		v := d.readMethodMeta()
		out[k] = v
	}
	return out
}

func (d *decoder) readSealedVariantSlice() []transpiler.SealedVariant {
	n := d.readUvarint()
	if d.err != nil || n == 0 {
		return nil
	}
	out := make([]transpiler.SealedVariant, n)
	for i := range out {
		out[i].Name = d.readString()
		out[i].Pos = d.readSourcePos()
		out[i].FieldNames = d.readStringSlice()
		out[i].FieldTypes = d.readTypeSlice()
	}
	return out
}

func (d *decoder) readTypeMeta() *transpiler.TypeMetadata {
	if !d.readBool() {
		return nil
	}
	t := &transpiler.TypeMetadata{}
	t.Name = d.readString()
	t.Package = d.readString()
	t.Pos = d.readSourcePos()
	t.Methods = d.readStringMethodMap()
	t.Fields = d.readStringTypeMap()
	t.FieldNames = d.readStringSlice()
	t.FieldPositions = d.readStringSourcePosMap()
	t.TypeParams = d.readStringSlice()
	t.TypeParamConstraints = d.readStringStringMap()
	t.ImmutFlags = d.readBoolSlice()
	t.IsSealed = d.readBool()
	t.SealedVariants = d.readSealedVariantSlice()
	t.DefinedIn = d.readString()
	return t
}

func (d *decoder) readStringTypeMetaMap() map[string]*transpiler.TypeMetadata {
	n := d.readUvarint()
	if d.err != nil || n == 0 {
		return nil
	}
	out := make(map[string]*transpiler.TypeMetadata, n)
	for i := uint64(0); i < n; i++ {
		k := d.readString()
		v := d.readTypeMeta()
		out[k] = v
	}
	return out
}

func (d *decoder) readFuncMeta() *transpiler.FunctionMetadata {
	if !d.readBool() {
		return nil
	}
	f := &transpiler.FunctionMetadata{}
	f.Name = d.readString()
	f.Package = d.readString()
	f.Pos = d.readSourcePos()
	f.ParamTypes = d.readTypeSlice()
	f.ParamNames = d.readStringSlice()
	f.ParamImmutFlags = d.readBoolSlice()
	f.ReturnType = d.readType()
	f.TypeParams = d.readStringSlice()
	dn := d.readUvarint()
	if dn > 0 {
		f.DefaultExprs = make(map[int]string, dn)
		for i := uint64(0); i < dn; i++ {
			idx := int(d.readInt32())
			f.DefaultExprs[idx] = d.readString()
		}
	}
	f.DefinedIn = d.readString()
	return f
}

func (d *decoder) readStringFuncMetaMap() map[string]*transpiler.FunctionMetadata {
	n := d.readUvarint()
	if d.err != nil || n == 0 {
		return nil
	}
	out := make(map[string]*transpiler.FunctionMetadata, n)
	for i := uint64(0); i < n; i++ {
		k := d.readString()
		v := d.readFuncMeta()
		out[k] = v
	}
	return out
}

func (d *decoder) readCompanion() *transpiler.CompanionObjectMetadata {
	if !d.readBool() {
		return nil
	}
	c := &transpiler.CompanionObjectMetadata{}
	c.Name = d.readString()
	c.Package = d.readString()
	c.TargetType = d.readString()
	n := d.readUvarint()
	if n > 0 {
		c.ExtractIndices = make([]int, n)
		for i := range c.ExtractIndices {
			c.ExtractIndices[i] = int(d.readInt32())
		}
	}
	return c
}

func (d *decoder) readStringCompanionMap() map[string]*transpiler.CompanionObjectMetadata {
	n := d.readUvarint()
	if d.err != nil || n == 0 {
		return nil
	}
	out := make(map[string]*transpiler.CompanionObjectMetadata, n)
	for i := uint64(0); i < n; i++ {
		k := d.readString()
		v := d.readCompanion()
		out[k] = v
	}
	return out
}

func (d *decoder) readGoExports() map[string][]string {
	n := d.readUvarint()
	if d.err != nil || n == 0 {
		return nil
	}
	out := make(map[string][]string, n)
	for i := uint64(0); i < n; i++ {
		k := d.readString()
		v := d.readStringSlice()
		out[k] = v
	}
	return out
}

func (d *decoder) readGoFuncSig() *transpiler.GoFuncSignature {
	if !d.readBool() {
		return nil
	}
	s := &transpiler.GoFuncSignature{}
	np := d.readUvarint()
	if np > 0 {
		s.Params = make([]transpiler.GoParam, np)
		for i := range s.Params {
			s.Params[i].Name = d.readString()
			s.Params[i].Type = d.readType()
		}
	}
	s.Returns = d.readTypeSlice()
	s.IsVariadic = d.readBool()
	s.TypeParams = d.readStringSlice()
	return s
}

func (d *decoder) readStringGoFuncSigMap() map[string]*transpiler.GoFuncSignature {
	n := d.readUvarint()
	if d.err != nil || n == 0 {
		return nil
	}
	out := make(map[string]*transpiler.GoFuncSignature, n)
	for i := uint64(0); i < n; i++ {
		k := d.readString()
		v := d.readGoFuncSig()
		out[k] = v
	}
	return out
}

func (d *decoder) readGoTypeData() *transpiler.GoTypeData {
	if !d.readBool() {
		return nil
	}
	t := &transpiler.GoTypeData{}
	t.Kind = d.readString()
	t.Fields = d.readStringTypeMap()
	t.FieldOrder = d.readStringSlice()
	t.Methods = d.readStringGoFuncSigMap()
	t.Underlying = d.readType()
	t.TypeParams = d.readStringSlice()
	return t
}

func (d *decoder) readStringGoTypeDataMap() map[string]*transpiler.GoTypeData {
	n := d.readUvarint()
	if d.err != nil || n == 0 {
		return nil
	}
	out := make(map[string]*transpiler.GoTypeData, n)
	for i := uint64(0); i < n; i++ {
		k := d.readString()
		v := d.readGoTypeData()
		out[k] = v
	}
	return out
}

func (d *decoder) readGoTypeInfoPtr() *transpiler.GoTypeInfo {
	if !d.readBool() {
		return nil
	}
	g := transpiler.NewGoTypeInfo()
	g.Functions = d.readStringGoFuncSigMap()
	if g.Functions == nil {
		g.Functions = make(map[string]*transpiler.GoFuncSignature)
	}
	g.Types = d.readStringGoTypeDataMap()
	if g.Types == nil {
		g.Types = make(map[string]*transpiler.GoTypeData)
	}
	g.Variables = d.readStringTypeMap()
	if g.Variables == nil {
		g.Variables = make(map[string]transpiler.Type)
	}
	g.Constants = d.readStringTypeMap()
	if g.Constants == nil {
		g.Constants = make(map[string]transpiler.Type)
	}
	g.TypeAliases = d.readStringTypeMap()
	if g.TypeAliases == nil {
		g.TypeAliases = make(map[string]transpiler.Type)
	}
	return g
}
