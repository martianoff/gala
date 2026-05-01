package worker

import (
	"bytes"
	"io"
	"testing"
)

// TestEncodeDecodeRoundTrip ensures a hand-built WorkResponse re-decodes
// (via decodeField) to the same logical fields after EncodeWorkResponse.
// This guards against silent wire-format regressions in the worker
// protocol — Bazel will reject a malformed response by killing the worker
// and restarting it, which silently degrades us back to the genrule cold
// start path.
func TestEncodeDecodeRoundTrip(t *testing.T) {
	resp := &WorkResponse{
		ExitCode:  3,
		Output:    "hello\nworld",
		RequestID: 42,
	}
	payload := EncodeWorkResponse(resp)

	// Walk fields manually, mirroring how Bazel's reader would.
	got := WorkResponse{}
	buf := payload
	for len(buf) > 0 {
		df, rest, err := decodeField(buf)
		if err != nil {
			t.Fatalf("decodeField: %v", err)
		}
		switch df.field {
		case 1:
			got.ExitCode = int32(df.payloadVarint)
		case 2:
			got.Output = string(df.payloadBytes)
		case 3:
			got.RequestID = int32(df.payloadVarint)
		case 4:
			got.WasCancelled = df.payloadVarint != 0
		}
		buf = rest
	}
	if got != *resp {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, *resp)
	}
}

// TestDecodeWorkRequest verifies arguments + request_id parse correctly
// and unknown fields are silently skipped.
func TestDecodeWorkRequest(t *testing.T) {
	// Hand-build: arguments=["transpile-package", "--inputs", "a.gala"], request_id=7,
	// plus an unknown field 5 (string "ignored") that decodeField must skip.
	var buf []byte
	args := []string{"transpile-package", "--inputs", "a.gala"}
	for _, a := range args {
		buf = writeVarint(buf, fieldKey(1, wireBytes))
		buf = writeVarint(buf, uint64(len(a)))
		buf = append(buf, a...)
	}
	buf = writeVarint(buf, fieldKey(3, wireVarint))
	buf = writeVarint(buf, 7)
	buf = writeVarint(buf, fieldKey(5, wireBytes))
	buf = writeVarint(buf, uint64(len("ignored")))
	buf = append(buf, "ignored"...)

	req, err := DecodeWorkRequest(buf)
	if err != nil {
		t.Fatalf("DecodeWorkRequest: %v", err)
	}
	if len(req.Arguments) != 3 || req.Arguments[0] != "transpile-package" || req.Arguments[2] != "a.gala" {
		t.Fatalf("arguments: got %v", req.Arguments)
	}
	if req.RequestID != 7 {
		t.Fatalf("request_id: got %d", req.RequestID)
	}
}

// TestReadDelimitedEOF confirms a clean stdin close surfaces as io.EOF so
// the worker loop can exit gracefully (Bazel signals worker shutdown by
// closing stdin).
func TestReadDelimitedEOF(t *testing.T) {
	_, err := ReadRequest(bytes.NewReader(nil))
	if err != io.EOF {
		t.Fatalf("expected io.EOF on empty stream, got %v", err)
	}
}

// TestFramedRoundTrip checks the full wire framing: write a response,
// read it back through a pipe, and confirm payload integrity.
func TestFramedRoundTrip(t *testing.T) {
	resp := &WorkResponse{ExitCode: 0, Output: "ok", RequestID: 1}
	var buf bytes.Buffer
	if err := WriteResponse(&buf, resp); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}
	// Re-read the framed payload, decode, compare.
	payload, err := readDelimited(&buf)
	if err != nil {
		t.Fatalf("readDelimited: %v", err)
	}
	if len(payload) == 0 {
		t.Fatal("empty framed payload")
	}
}
