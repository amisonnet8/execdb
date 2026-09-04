package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Pre-startup request codes a client may send instead of a real
// StartupMessage (.claude/rules/pgwire.md's authentication-handshake
// scope). protocolVersion3 is StartupMessage's own "code" field
// (0x00030000): protocol major 3, minor 0.
const (
	codeSSLRequest    = 80877103
	codeGSSENCRequest = 80877104
	codeCancelRequest = 80877102
	protocolVersion3  = 196608
)

// readStartupHeader reads the 8-byte header common to every possible
// first message on a new connection (StartupMessage, SSLRequest,
// GSSENCRequest, CancelRequest): an Int32 length (including itself) and
// an Int32 code (either a request code above, or the protocol version).
func readStartupHeader(r io.Reader) (length int32, code int32, err error) {
	var hdr [8]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, 0, err
	}
	return int32(binary.BigEndian.Uint32(hdr[0:4])), int32(binary.BigEndian.Uint32(hdr[4:8])), nil
}

// readStartupParams reads a StartupMessage's key/value parameter list (n
// bytes, the length already declared by readStartupHeader minus its own
// 8-byte header): pairs of null-terminated strings, terminated by one
// final zero byte.
func readStartupParams(r io.Reader, n int) (map[string]string, error) {
	if n < 0 {
		return nil, fmt.Errorf("pgwire: negative startup parameter length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}

	params := map[string]string{}
	i := 0
	for i < len(buf) && buf[i] != 0 {
		key, next, ok := readCString(buf, i)
		if !ok {
			return nil, errors.New("pgwire: malformed startup parameters")
		}
		val, next2, ok := readCString(buf, next)
		if !ok {
			return nil, errors.New("pgwire: malformed startup parameters")
		}
		params[key] = val
		i = next2
	}
	return params, nil
}

func readCString(buf []byte, start int) (s string, next int, ok bool) {
	for i := start; i < len(buf); i++ {
		if buf[i] == 0 {
			return string(buf[start:i]), i + 1, true
		}
	}
	return "", 0, false
}

// frontendMessage is one regular (post-startup) message from the client:
// a 1-byte type plus an Int32 length (including itself) plus a body.
type frontendMessage struct {
	Type byte
	Body []byte
}

func readFrontendMessage(r io.Reader) (*frontendMessage, error) {
	var typ [1]byte
	if _, err := io.ReadFull(r, typ[:]); err != nil {
		return nil, err
	}
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	length := int32(binary.BigEndian.Uint32(lenBuf[:]))
	if length < 4 {
		return nil, fmt.Errorf("pgwire: invalid message length %d", length)
	}
	body := make([]byte, length-4)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return &frontendMessage{Type: typ[0], Body: body}, nil
}

// cStringFromBody extracts a Query message's null-terminated query string
// from its body.
func cStringFromBody(body []byte) string {
	for i, c := range body {
		if c == 0 {
			return string(body[:i])
		}
	}
	return string(body)
}

// --- backend (server-to-client) message writers ---
//
// SQLSTATE codes are simplified per spec §8 ("詳細なSQLSTATEコード体系
// ...簡略化したコードで代用"): sqlstateInsufficientPrivilege (42501) is
// spec §8's own choice for the access-control rejection; sqlstateGeneric
// is this implementation's stand-in for every other backend error, since
// phase 1 does not build a real SQLite-error-to-SQLSTATE mapping.
const (
	sqlstateInsufficientPrivilege = "42501"
	sqlstateGeneric               = "XX000"
)

func writeAuthenticationOk(w io.Writer) error {
	return writeMessage(w, 'R', func(b *msgBuilder) { b.int32(0) })
}

func writeParameterStatus(w io.Writer, name, value string) error {
	return writeMessage(w, 'S', func(b *msgBuilder) {
		b.cstring(name)
		b.cstring(value)
	})
}

func writeBackendKeyData(w io.Writer, pid, secret int32) error {
	return writeMessage(w, 'K', func(b *msgBuilder) {
		b.int32(pid)
		b.int32(secret)
	})
}

func writeReadyForQuery(w io.Writer, status byte) error {
	return writeMessage(w, 'Z', func(b *msgBuilder) { b.byte(status) })
}

// writeRowDescription describes cols. Phase 1 does not implement a type
// mapping (.claude/rules/pgwire.md: "決め打ちで実装せず実接続で確定"), so
// every column is reported as OID 25 (text) in text format, matching
// execSQL's/pgwire's own text-only value formatting.
func writeRowDescription(w io.Writer, cols []string) error {
	return writeMessage(w, 'T', func(b *msgBuilder) {
		b.int16(int16(len(cols)))
		for _, name := range cols {
			b.cstring(name)
			b.int32(0)  // table OID: none
			b.int16(0)  // column attribute number: none
			b.int32(25) // data type OID: text
			b.int16(-1) // data type size: variable-length
			b.int32(-1) // type modifier: none
			b.int16(0)  // format code: text
		}
	})
}

// writeDataRow sends one row. A nil entry in values encodes SQL NULL
// (length -1, no bytes); the driver in this package produces values via
// formatValue (format.go), the same stringification used by REPL output.
func writeDataRow(w io.Writer, values []*string) error {
	return writeMessage(w, 'D', func(b *msgBuilder) {
		b.int16(int16(len(values)))
		for _, v := range values {
			if v == nil {
				b.int32(-1)
				continue
			}
			data := []byte(*v)
			b.int32(int32(len(data)))
			b.raw(data)
		}
	})
}

func writeCommandComplete(w io.Writer, tag string) error {
	return writeMessage(w, 'C', func(b *msgBuilder) { b.cstring(tag) })
}

func writeErrorResponse(w io.Writer, sqlstate, message string) error {
	return writeMessage(w, 'E', func(b *msgBuilder) {
		b.byte('S')
		b.cstring("ERROR")
		b.byte('C')
		b.cstring(sqlstate)
		b.byte('M')
		b.cstring(message)
		b.byte(0)
	})
}

// msgBuilder accumulates one message's body; writeMessage prefixes it
// with the type byte and the Int32 length PostgreSQL's wire protocol
// requires (the length field counts itself but not the leading type
// byte).
type msgBuilder struct {
	buf []byte
}

func (b *msgBuilder) byte(v byte)  { b.buf = append(b.buf, v) }
func (b *msgBuilder) raw(v []byte) { b.buf = append(b.buf, v...) }

func (b *msgBuilder) cstring(s string) {
	b.buf = append(b.buf, s...)
	b.buf = append(b.buf, 0)
}

func (b *msgBuilder) int16(v int16) {
	var tmp [2]byte
	binary.BigEndian.PutUint16(tmp[:], uint16(v))
	b.buf = append(b.buf, tmp[:]...)
}

func (b *msgBuilder) int32(v int32) {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], uint32(v))
	b.buf = append(b.buf, tmp[:]...)
}

func writeMessage(w io.Writer, typ byte, fn func(*msgBuilder)) error {
	b := &msgBuilder{}
	fn(b)
	out := make([]byte, 0, 5+len(b.buf))
	out = append(out, typ)
	var lenField [4]byte
	binary.BigEndian.PutUint32(lenField[:], uint32(4+len(b.buf)))
	out = append(out, lenField[:]...)
	out = append(out, b.buf...)
	_, err := w.Write(out)
	return err
}
