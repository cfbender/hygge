package mcp

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestWriteReadFrame_Roundtrip(t *testing.T) {
	t.Parallel()
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"hello"}`)
	var buf bytes.Buffer
	n, err := WriteFrame(&buf, body)
	if err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	if n != buf.Len() {
		t.Fatalf("WriteFrame returned %d but wrote %d", n, buf.Len())
	}

	r := bufio.NewReader(&buf)
	got, err := ReadFrame(r)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("body mismatch: got %q want %q", got, body)
	}
}

func TestReadFrame_MultipleBackToBack(t *testing.T) {
	t.Parallel()
	bodies := [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":1}`),
		[]byte(`{"jsonrpc":"2.0","method":"a"}`),
		[]byte(`{"jsonrpc":"2.0","id":2,"result":{"x":1}}`),
	}
	var buf bytes.Buffer
	for _, b := range bodies {
		if _, err := WriteFrame(&buf, b); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
	}
	r := bufio.NewReader(&buf)
	for i, want := range bodies {
		got, err := ReadFrame(r)
		if err != nil {
			t.Fatalf("ReadFrame[%d]: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("ReadFrame[%d]: got %q want %q", i, got, want)
		}
	}
	// Next read at EOF should be clean.
	if _, err := ReadFrame(r); !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF at end, got %v", err)
	}
}

func TestReadFrame_MissingContentLength(t *testing.T) {
	t.Parallel()
	input := "X-Other: 1\r\n\r\n{}"
	r := bufio.NewReader(strings.NewReader(input))
	_, err := ReadFrame(r)
	if !errors.Is(err, ErrMalformedFrame) {
		t.Fatalf("expected ErrMalformedFrame, got %v", err)
	}
}

func TestReadFrame_BodyShorterThanContentLength(t *testing.T) {
	t.Parallel()
	input := "Content-Length: 50\r\n\r\n{}"
	r := bufio.NewReader(strings.NewReader(input))
	_, err := ReadFrame(r)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestReadFrame_ToleratesContentType(t *testing.T) {
	t.Parallel()
	body := []byte(`{"jsonrpc":"2.0","id":1}`)
	input := "Content-Length: " + itoa(len(body)) + "\r\nContent-Type: application/vscode-jsonrpc; charset=utf-8\r\n\r\n" + string(body)
	r := bufio.NewReader(strings.NewReader(input))
	got, err := ReadFrame(r)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("body mismatch: got %q want %q", got, body)
	}
}

func TestReadFrame_InvalidContentLength(t *testing.T) {
	t.Parallel()
	input := "Content-Length: foo\r\n\r\n{}"
	r := bufio.NewReader(strings.NewReader(input))
	_, err := ReadFrame(r)
	if !errors.Is(err, ErrMalformedFrame) {
		t.Fatalf("expected ErrMalformedFrame, got %v", err)
	}
}

func TestReadFrame_InvalidHeaderLine(t *testing.T) {
	t.Parallel()
	input := "garbage-no-colon\r\n\r\n"
	r := bufio.NewReader(strings.NewReader(input))
	_, err := ReadFrame(r)
	if !errors.Is(err, ErrMalformedFrame) {
		t.Fatalf("expected ErrMalformedFrame, got %v", err)
	}
}

func TestReadFrame_EOFInHeader(t *testing.T) {
	t.Parallel()
	input := "Content-Length: 5\r\n"
	r := bufio.NewReader(strings.NewReader(input))
	_, err := ReadFrame(r)
	if !errors.Is(err, ErrMalformedFrame) {
		t.Fatalf("expected ErrMalformedFrame, got %v", err)
	}
}

// itoa avoids importing strconv just for the test fixture.
func itoa(n int) string {
	return strings.TrimSpace(intToString(n))
}

func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ---------------------------------------------------------------------------
// NDJSON framing
// ---------------------------------------------------------------------------

func TestWriteReadNDJSON_Roundtrip(t *testing.T) {
	t.Parallel()
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"hello"}`)
	var buf bytes.Buffer
	n, err := WriteNDJSON(&buf, body)
	if err != nil {
		t.Fatalf("WriteNDJSON: %v", err)
	}
	if n != len(body)+1 {
		t.Fatalf("WriteNDJSON returned %d, want %d", n, len(body)+1)
	}
	// The written bytes should be body + '\n'.
	if raw := buf.Bytes(); raw[len(raw)-1] != '\n' {
		t.Fatalf("WriteNDJSON did not append newline: %q", raw)
	}

	r := bufio.NewReader(&buf)
	got, err := ReadNDJSON(r)
	if err != nil {
		t.Fatalf("ReadNDJSON: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("body mismatch: got %q want %q", got, body)
	}
}

func TestWriteReadNDJSON_MultipleBackToBack(t *testing.T) {
	t.Parallel()
	bodies := [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":1}`),
		[]byte(`{"jsonrpc":"2.0","method":"ping"}`),
		[]byte(`{"jsonrpc":"2.0","id":2,"result":{}}`),
	}
	var buf bytes.Buffer
	for _, b := range bodies {
		if _, err := WriteNDJSON(&buf, b); err != nil {
			t.Fatalf("WriteNDJSON: %v", err)
		}
	}
	r := bufio.NewReader(&buf)
	for i, want := range bodies {
		got, err := ReadNDJSON(r)
		if err != nil {
			t.Fatalf("ReadNDJSON[%d]: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("ReadNDJSON[%d]: got %q want %q", i, got, want)
		}
	}
	// Next read at clean EOF should return io.EOF.
	if _, err := ReadNDJSON(r); !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF at end, got %v", err)
	}
}

func TestReadNDJSON_CleanEOF(t *testing.T) {
	t.Parallel()
	r := bufio.NewReader(strings.NewReader(""))
	_, err := ReadNDJSON(r)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF on empty reader, got %v", err)
	}
}

func TestReadNDJSON_TruncatedLine(t *testing.T) {
	t.Parallel()
	// No trailing newline — truncated message.
	r := bufio.NewReader(strings.NewReader(`{"jsonrpc":"2.0"}`))
	_, err := ReadNDJSON(r)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestReadNDJSON_ToleratesCRLF(t *testing.T) {
	t.Parallel()
	body := []byte(`{"jsonrpc":"2.0","id":1}`)
	input := string(body) + "\r\n"
	r := bufio.NewReader(strings.NewReader(input))
	got, err := ReadNDJSON(r)
	if err != nil {
		t.Fatalf("ReadNDJSON: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("body mismatch: got %q want %q", got, body)
	}
}

func TestReadNDJSON_RejectsOversizedLineBeforeNewline(t *testing.T) {
	t.Parallel()
	r := bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", maxFrameSize+2)), 16)
	_, err := ReadNDJSON(r)
	if !errors.Is(err, ErrMalformedFrame) {
		t.Fatalf("expected ErrMalformedFrame, got %v", err)
	}
}
