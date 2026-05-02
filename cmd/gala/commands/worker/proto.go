// Package worker implements the Bazel persistent-worker protocol for gala
// transpile actions.
//
// Protocol summary (https://bazel.build/remote/persistent and the
// google/devtools/build/lib/worker/worker_protocol.proto definitions):
//
//   - Bazel launches one worker process per `worker_key`. The same process
//     is reused across many actions within a build invocation, so analyzer
//     warm-up (loading the std/collection_immutable type graph) amortizes
//     across all transpile actions instead of being paid once per package.
//   - Bazel writes length-delimited `WorkRequest` protobuf messages to the
//     worker's stdin. Each request carries the per-action `arguments` —
//     everything that would have been on the command line in non-worker
//     mode (including --input/--output/--search etc.).
//   - The worker writes a length-delimited `WorkResponse` (request_id,
//     exit_code, output) per request to stdout. Stdout MUST be reserved
//     for protobuf framing; ALL diagnostics must go via the response's
//     `output` field or to stderr (Bazel ignores worker stderr in
//     successful runs and surfaces it on failure).
//
// We intentionally hand-roll the wire encoder/decoder for the two message
// shapes we care about rather than depend on google.golang.org/protobuf:
// the runtime needs only varint / string / bool wire types (5 fields
// across both messages) and adding the protobuf module to MODULE.bazel
// would touch the dependency graph in a way unrelated to the worker
// feature itself. See worker_test.go for round-trip coverage against
// hand-crafted byte sequences.
package worker

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// WorkRequest mirrors the subset of blaze.worker.WorkRequest that the gala
// worker reads. Fields we don't use (inputs, sandbox_dir, verbosity) are
// silently skipped during decoding.
type WorkRequest struct {
	Arguments []string // field 1, repeated string
	RequestID int32    // field 3, int32
	Cancel    bool     // field 4, bool
}

// WorkResponse mirrors the subset of blaze.worker.WorkResponse the worker
// produces. `Output` carries any human-readable diagnostics for the action
// (stdout/stderr equivalent in non-worker mode). `WasCancelled` is set
// when the worker observed a cancel request before completing.
type WorkResponse struct {
	ExitCode     int32  // field 1, int32
	Output       string // field 2, string
	RequestID    int32  // field 3, int32
	WasCancelled bool   // field 4, bool
}

// Wire types we use.
const (
	wireVarint = 0
	wireBytes  = 2
)

// readVarint reads a base-128 varint from r. Bazel's Java worker only ever
// emits unsigned varints up to 64 bits for our message shapes; we consume
// up to 10 bytes which is the maximum varint length for a uint64.
func readVarint(r io.ByteReader) (uint64, error) {
	var x uint64
	var s uint
	for i := 0; i < 10; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		if b < 0x80 {
			if i == 9 && b > 1 {
				return 0, errors.New("worker: varint overflow")
			}
			return x | uint64(b)<<s, nil
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
	return 0, errors.New("worker: varint too long")
}

// writeVarint encodes x as a base-128 varint and appends it to buf.
func writeVarint(buf []byte, x uint64) []byte {
	for x >= 0x80 {
		buf = append(buf, byte(x)|0x80)
		x >>= 7
	}
	return append(buf, byte(x))
}

// fieldKey encodes (field_number, wire_type) the way protobuf does:
// (field_number << 3) | wire_type.
func fieldKey(field, wire uint64) uint64 { return field<<3 | wire }

// readByteReader adapts io.Reader to io.ByteReader for varint decoding.
// We cannot rely on bufio.Reader because the caller already framed the
// payload via a length prefix and we want to consume EXACTLY that many
// bytes — using a *bytes.Reader keeps that guarantee.
type byteReader struct{ r io.Reader }

func (br *byteReader) ReadByte() (byte, error) {
	var b [1]byte
	_, err := io.ReadFull(br.r, b[:])
	return b[0], err
}

// readDelimited reads one length-delimited protobuf message from r.
// Returns the message payload (without the length prefix) or an error.
// io.EOF on a zero-byte read at the start signals clean shutdown.
func readDelimited(r io.Reader) ([]byte, error) {
	br := &byteReader{r: r}
	n, err := readVarint(br)
	if err != nil {
		return nil, err
	}
	if n > 1<<30 {
		return nil, fmt.Errorf("worker: oversized message: %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// writeDelimited writes a length-prefixed protobuf message to w.
func writeDelimited(w io.Writer, payload []byte) error {
	prefix := writeVarint(make([]byte, 0, binary.MaxVarintLen64), uint64(len(payload)))
	if _, err := w.Write(prefix); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// decodeField is one step of message decoding: it pulls a (key, payload)
// pair out of buf and returns the remaining slice. For varint fields,
// payloadVarint holds the value; for length-delimited fields, payloadBytes
// holds the bytes; for unknown wire types, both are zero/nil and the
// caller is expected to skip.
type decodedField struct {
	field         uint64
	wire          uint64
	payloadVarint uint64
	payloadBytes  []byte
}

func decodeField(buf []byte) (decodedField, []byte, error) {
	r := &readerBytes{buf: buf}
	key, err := readVarint(r)
	if err != nil {
		return decodedField{}, nil, err
	}
	field := key >> 3
	wire := key & 0x7
	df := decodedField{field: field, wire: wire}
	switch wire {
	case wireVarint:
		v, err := readVarint(r)
		if err != nil {
			return df, nil, err
		}
		df.payloadVarint = v
	case wireBytes:
		n, err := readVarint(r)
		if err != nil {
			return df, nil, err
		}
		if uint64(len(r.buf)) < n {
			return df, nil, errors.New("worker: short length-delimited payload")
		}
		df.payloadBytes = r.buf[:n]
		r.buf = r.buf[n:]
	default:
		return df, nil, fmt.Errorf("worker: unsupported wire type %d on field %d", wire, field)
	}
	return df, r.buf, nil
}

// readerBytes is a byte slice that satisfies io.ByteReader without an
// extra allocation per varint — readVarint pulls one byte at a time.
type readerBytes struct{ buf []byte }

func (r *readerBytes) ReadByte() (byte, error) {
	if len(r.buf) == 0 {
		return 0, io.EOF
	}
	b := r.buf[0]
	r.buf = r.buf[1:]
	return b, nil
}

// DecodeWorkRequest parses a serialized WorkRequest payload (no length
// prefix — pair with readDelimited).
func DecodeWorkRequest(buf []byte) (*WorkRequest, error) {
	req := &WorkRequest{}
	for len(buf) > 0 {
		df, rest, err := decodeField(buf)
		if err != nil {
			return nil, err
		}
		switch df.field {
		case 1: // arguments (repeated string)
			if df.wire != wireBytes {
				return nil, fmt.Errorf("worker: bad wire on arguments: %d", df.wire)
			}
			req.Arguments = append(req.Arguments, string(df.payloadBytes))
		case 3: // request_id
			if df.wire != wireVarint {
				return nil, fmt.Errorf("worker: bad wire on request_id: %d", df.wire)
			}
			req.RequestID = int32(df.payloadVarint)
		case 4: // cancel
			if df.wire != wireVarint {
				return nil, fmt.Errorf("worker: bad wire on cancel: %d", df.wire)
			}
			req.Cancel = df.payloadVarint != 0
		default:
			// inputs (field 2), sandbox_dir (5), verbosity (6) etc. — skip.
		}
		buf = rest
	}
	return req, nil
}

// EncodeWorkResponse serializes resp to a protobuf wire-format payload (no
// length prefix — pair with writeDelimited).
func EncodeWorkResponse(resp *WorkResponse) []byte {
	buf := make([]byte, 0, 32+len(resp.Output))
	if resp.ExitCode != 0 {
		buf = writeVarint(buf, fieldKey(1, wireVarint))
		buf = writeVarint(buf, uint64(int64(resp.ExitCode)))
	}
	if resp.Output != "" {
		buf = writeVarint(buf, fieldKey(2, wireBytes))
		buf = writeVarint(buf, uint64(len(resp.Output)))
		buf = append(buf, resp.Output...)
	}
	if resp.RequestID != 0 {
		buf = writeVarint(buf, fieldKey(3, wireVarint))
		buf = writeVarint(buf, uint64(int64(resp.RequestID)))
	}
	if resp.WasCancelled {
		buf = writeVarint(buf, fieldKey(4, wireVarint))
		buf = writeVarint(buf, 1)
	}
	return buf
}

// ReadRequest pulls one WorkRequest off r. Returns io.EOF on clean
// end-of-stream (Bazel closing stdin).
func ReadRequest(r io.Reader) (*WorkRequest, error) {
	payload, err := readDelimited(r)
	if err != nil {
		return nil, err
	}
	return DecodeWorkRequest(payload)
}

// WriteResponse frames and writes resp to w.
func WriteResponse(w io.Writer, resp *WorkResponse) error {
	return writeDelimited(w, EncodeWorkResponse(resp))
}
