package mcp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// maxFrameSize caps the body of a single framed message at 32 MiB.
// MCP servers in the wild don't approach this; the cap exists so a
// rogue server can't trigger an unbounded allocation.
const maxFrameSize = 32 * 1024 * 1024

// WriteFrame writes one JSON-RPC message to w with Content-Length
// framing.  Returns the total number of bytes written (header + body).
// The function does not flush w; callers that buffer their writer must
// flush after the call returns.
func WriteFrame(w io.Writer, body []byte) (int, error) {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	hn, err := io.WriteString(w, header)
	if err != nil {
		return hn, err
	}
	bn, err := w.Write(body)
	if err != nil {
		return hn + bn, err
	}
	return hn + bn, nil
}

// ReadFrame reads one Content-Length-framed JSON-RPC message from r and
// returns the body bytes (without the header).  io.EOF is returned
// cleanly when r reports EOF at a frame boundary (no header read yet).
// Any malformed header or short body returns ErrMalformedFrame wrapped
// with context.
func ReadFrame(r *bufio.Reader) ([]byte, error) {
	contentLength := -1
	headerRead := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) && !headerRead && line == "" {
				// Clean EOF at a frame boundary.
				return nil, io.EOF
			}
			if errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("%w: unexpected EOF in header", ErrMalformedFrame)
			}
			return nil, err
		}
		headerRead = true
		// Header lines must end with CRLF.
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			// Blank line — end of header block.
			break
		}
		// Tolerate any header; only Content-Length is meaningful.
		colon := strings.IndexByte(trimmed, ':')
		if colon <= 0 {
			return nil, fmt.Errorf("%w: invalid header line %q", ErrMalformedFrame, trimmed)
		}
		key := strings.TrimSpace(trimmed[:colon])
		val := strings.TrimSpace(trimmed[colon+1:])
		if strings.EqualFold(key, "Content-Length") {
			n, err := strconv.Atoi(val)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("%w: invalid Content-Length %q", ErrMalformedFrame, val)
			}
			if n > maxFrameSize {
				return nil, fmt.Errorf("%w: Content-Length %d exceeds max %d", ErrMalformedFrame, n, maxFrameSize)
			}
			contentLength = n
		}
		// Other headers (e.g. Content-Type) are silently accepted.
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("%w: missing Content-Length", ErrMalformedFrame)
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return body, nil
}
