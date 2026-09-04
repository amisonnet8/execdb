package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestWriteAuthenticationOk(t *testing.T) {
	var buf bytes.Buffer
	if err := writeAuthenticationOk(&buf); err != nil {
		t.Fatal(err)
	}
	want := []byte{'R', 0, 0, 0, 8, 0, 0, 0, 0}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("got % x, want % x", buf.Bytes(), want)
	}
}

func TestWriteReadyForQuery(t *testing.T) {
	var buf bytes.Buffer
	if err := writeReadyForQuery(&buf, 'I'); err != nil {
		t.Fatal(err)
	}
	want := []byte{'Z', 0, 0, 0, 5, 'I'}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("got % x, want % x", buf.Bytes(), want)
	}
}

func TestWriteParameterStatus(t *testing.T) {
	var buf bytes.Buffer
	if err := writeParameterStatus(&buf, "a", "b"); err != nil {
		t.Fatal(err)
	}
	want := []byte{'S', 0, 0, 0, 8, 'a', 0, 'b', 0}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("got % x, want % x", buf.Bytes(), want)
	}
}

func TestWriteCommandComplete(t *testing.T) {
	var buf bytes.Buffer
	if err := writeCommandComplete(&buf, "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if buf.Bytes()[0] != 'C' {
		t.Fatalf("expected type byte 'C', got %q", buf.Bytes()[0])
	}
	length := binary.BigEndian.Uint32(buf.Bytes()[1:5])
	if int(length) != buf.Len()-1 {
		t.Errorf("length field = %d, want %d", length, buf.Len()-1)
	}
	if string(buf.Bytes()[5:]) != "SELECT 1\x00" {
		t.Errorf("body = %q", buf.Bytes()[5:])
	}
}

func TestWriteRowDescription(t *testing.T) {
	var buf bytes.Buffer
	if err := writeRowDescription(&buf, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if buf.Bytes()[0] != 'T' {
		t.Fatalf("expected type byte 'T', got %q", buf.Bytes()[0])
	}
	body := buf.Bytes()[5:]
	numFields := binary.BigEndian.Uint16(body[0:2])
	if numFields != 2 {
		t.Fatalf("numFields = %d, want 2", numFields)
	}
}

func TestWriteDataRow(t *testing.T) {
	var buf bytes.Buffer
	v := "hello"
	if err := writeDataRow(&buf, []*string{&v, nil}); err != nil {
		t.Fatal(err)
	}
	if buf.Bytes()[0] != 'D' {
		t.Fatalf("expected type byte 'D', got %q", buf.Bytes()[0])
	}

	body := buf.Bytes()[5:]
	numFields := binary.BigEndian.Uint16(body[0:2])
	if numFields != 2 {
		t.Fatalf("numFields = %d, want 2", numFields)
	}
	f1Len := int32(binary.BigEndian.Uint32(body[2:6]))
	if f1Len != 5 || string(body[6:11]) != "hello" {
		t.Errorf("field 1 mismatch: len=%d data=%q", f1Len, body[6:11])
	}
	f2Len := int32(binary.BigEndian.Uint32(body[11:15]))
	if f2Len != -1 {
		t.Errorf("field 2 (NULL) length = %d, want -1", f2Len)
	}
}

func TestWriteErrorResponse(t *testing.T) {
	var buf bytes.Buffer
	if err := writeErrorResponse(&buf, "42501", "nope"); err != nil {
		t.Fatal(err)
	}
	if buf.Bytes()[0] != 'E' {
		t.Fatalf("expected type byte 'E', got %q", buf.Bytes()[0])
	}
	body := buf.Bytes()[5:]
	if !bytes.Contains(body, []byte("42501")) || !bytes.Contains(body, []byte("nope")) {
		t.Errorf("body missing expected fields: %q", body)
	}
}

func TestReadFrontendMessageRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte('Q')
	payload := []byte("SELECT 1;\x00")
	var lenField [4]byte
	binary.BigEndian.PutUint32(lenField[:], uint32(4+len(payload)))
	buf.Write(lenField[:])
	buf.Write(payload)

	msg, err := readFrontendMessage(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Type != 'Q' {
		t.Errorf("Type = %q, want 'Q'", msg.Type)
	}
	if string(msg.Body) != string(payload) {
		t.Errorf("Body = %q, want %q", msg.Body, payload)
	}
	if got := cStringFromBody(msg.Body); got != "SELECT 1;" {
		t.Errorf("cStringFromBody = %q, want %q", got, "SELECT 1;")
	}
}

func TestReadStartupHeaderAndParams(t *testing.T) {
	var buf bytes.Buffer
	params := []byte("user\x00alice\x00database\x00mydb\x00\x00")
	total := 8 + len(params)
	var lenField, codeField [4]byte
	binary.BigEndian.PutUint32(lenField[:], uint32(total))
	binary.BigEndian.PutUint32(codeField[:], uint32(protocolVersion3))
	buf.Write(lenField[:])
	buf.Write(codeField[:])
	buf.Write(params)

	length, code, err := readStartupHeader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if int(length) != total {
		t.Errorf("length = %d, want %d", length, total)
	}
	if code != protocolVersion3 {
		t.Errorf("code = %d, want %d", code, protocolVersion3)
	}

	got, err := readStartupParams(&buf, int(length)-8)
	if err != nil {
		t.Fatal(err)
	}
	if got["user"] != "alice" || got["database"] != "mydb" {
		t.Errorf("params = %v", got)
	}
}
