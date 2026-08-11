package pv3

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/pierrec/lz4/v4"
)

func fixture(t *testing.T, parts ...string) []byte {
	t.Helper()
	p := append([]string{"..", "..", "..", "Discovery", "codex", "captures"}, parts...)
	b, err := os.ReadFile(filepath.Join(p...))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseCapturedStatus(t *testing.T) {
	raw := fixture(t, "2026-08-10-passive-01", "tablet-to-server.bin")
	rec, err := ReadRecord(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	s, err := ParseStatus(rec)
	if err != nil {
		t.Fatal(err)
	}
	if s.UUID != "30004f00-0650-4858-5239-312000000000" {
		t.Fatalf("uuid %s", s.UUID)
	}
	if s.Width != 1024 || s.Height != 758 || s.Battery != 100 || s.Firmware != "7.4.4407" {
		t.Fatalf("unexpected status: %+v", s)
	}
}

func TestParseCapturedAcknowledgements(t *testing.T) {
	for _, tc := range []struct {
		file     string
		sequence uint32
	}{{"01-type-3-length-64.bin", 3}, {"02-type-3-length-64.bin", 4}} {
		raw := fixture(t, "2026-08-10-vss-usb-disconnected-01", "tablet-records", tc.file)
		rec, err := ReadRecord(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		messageType, err := ApplicationMessageType(rec)
		if err != nil || messageType != MessageAcknowledgement {
			t.Fatalf("%s message type=%d err=%v", tc.file, messageType, err)
		}
		ack, err := ParseAcknowledgement(rec)
		if err != nil {
			t.Fatal(err)
		}
		if ack.UUID != "30004f00-0650-4858-5239-312000000000" || ack.Sequence != tc.sequence {
			t.Fatalf("%s acknowledgement=%+v", tc.file, ack)
		}
	}
}

func TestRejectMalformedAcknowledgement(t *testing.T) {
	raw := fixture(t, "2026-08-10-vss-usb-disconnected-01", "tablet-records", "01-type-3-length-64.bin")
	rec, err := ReadRecord(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	rec.Payload[28] = 9
	if _, err := ParseAcknowledgement(rec); err == nil {
		t.Fatal("malformed acknowledgement accepted")
	}
}

func TestParseCapturedTouchEvents(t *testing.T) {
	tests := []struct {
		capture string
		file    string
		rawX    uint32
		rawY    uint32
		frameID uint32
		x       uint32
		y       uint32
	}{
		{"2026-08-11-touch-01", "02-type-3-length-76.bin", 924, 703, 0, 99, 54},
		{"2026-08-11-touch-01", "03-type-3-length-76.bin", 491, 399, 0, 532, 358},
		{"2026-08-11-touch-01", "04-type-3-length-76.bin", 100, 37, 0, 923, 720},
		{"2026-08-11-touch-02", "01-type-3-length-76.bin", 512, 415, 0, 511, 342},
		{"2026-08-11-touch-02", "02-type-3-length-76.bin", 968, 413, 0, 55, 344},
		{"2026-08-11-touch-server-retest-01", "01-type-3-length-76.bin", 508, 417, 0xeb3c1ea7, 515, 340},
	}
	for _, tc := range tests {
		raw := fixture(t, tc.capture, "tablet-records", tc.file)
		rec, err := ReadRecord(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		touch, err := ParseTouch(rec)
		if err != nil {
			t.Fatal(err)
		}
		if touch.UUID != "30004f00-0650-4858-5239-312000000000" || touch.RawX != tc.rawX || touch.RawY != tc.rawY || touch.FrameID != tc.frameID {
			t.Fatalf("%s touch=%+v", tc.file, touch)
		}
		x, y, err := touch.PhysicalCoordinates(1024, 758)
		if err != nil || x != tc.x || y != tc.y {
			t.Fatalf("%s physical=(%d,%d) err=%v, want (%d,%d)", tc.file, x, y, err, tc.x, tc.y)
		}
	}
}

func TestRejectMalformedTouch(t *testing.T) {
	raw := fixture(t, "2026-08-11-touch-01", "tablet-records", "02-type-3-length-76.bin")
	rec, err := ReadRecord(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	rec.Payload[40] = 1
	if _, err := ParseTouch(rec); err == nil {
		t.Fatal("malformed touch accepted")
	}
	if _, _, err := (Touch{RawX: 1024, RawY: 1}).PhysicalCoordinates(1024, 758); err == nil {
		t.Fatal("out-of-bounds touch accepted")
	}
}

func TestReadRecordHandlesFragmentation(t *testing.T) {
	raw := fixture(t, "2026-08-10-vss-heartbeat-01", "01-frame-1-tablet-to-server.bin")
	r := &oneByteReader{data: raw}
	rec, err := ReadRecord(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Payload) != 532 {
		t.Fatalf("payload=%d", len(rec.Payload))
	}
}

type oneByteReader struct{ data []byte }

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	p[0] = r.data[0]
	r.data = r.data[1:]
	return 1, nil
}

func TestStatusResponseShape(t *testing.T) {
	var s Status
	s.Kind = 3
	copy(s.UUIDBytes[:], []byte("0123456789abcdef"))
	wire, err := StatusResponse(s, false)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := ReadRecord(bytes.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Word2 != 1 || len(rec.Payload) != 44 || binary.LittleEndian.Uint32(rec.Payload[24:28]) != 3 {
		t.Fatalf("bad response")
	}
}

func TestBuildImage(t *testing.T) {
	pixels := bytes.Repeat([]byte{0xf0}, 1024*758/2)
	var id [16]byte
	copy(id[:], []byte("0123456789abcdef"))
	wire, err := BuildImage(id, 4, 0x12345678, 1024, 758, pixels)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := ReadRecord(bytes.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	logical := decompress(t, rec.Payload)
	if len(logical) != 80+len(pixels) {
		t.Fatalf("logical=%d", len(logical))
	}
	if binary.LittleEndian.Uint32(logical[36:40]) != 0x12345678 {
		t.Fatal("frame id")
	}
	if binary.LittleEndian.Uint32(logical[72:76]) != 4 {
		t.Fatal("encoding")
	}
	if !bytes.Equal(logical[80:], pixels) {
		t.Fatal("pixels changed")
	}
	if binary.LittleEndian.Uint32(wire[16:20]) != crc32.ChecksumIEEE(rec.Payload) {
		t.Fatal("outer checksum")
	}
}

func TestIncompressibleChunkUsesLiteralBlock(t *testing.T) {
	src := make([]byte, chunkSize)
	for i := range src {
		src[i] = byte(rand.Uint32())
	}
	payload, err := CompressChunks(src)
	if err != nil {
		t.Fatal(err)
	}
	if got := decompress(t, payload); !bytes.Equal(got, src) {
		t.Fatal("literal block changed data")
	}
}

func decompress(t *testing.T, payload []byte) []byte {
	t.Helper()
	var out []byte
	for off := 0; off < len(payload); {
		if off+24 > len(payload) {
			t.Fatal("short chunk")
		}
		n := int(binary.LittleEndian.Uint32(payload[off+8 : off+12]))
		u := int(binary.LittleEndian.Uint32(payload[off+12 : off+16]))
		off += 24
		if off+n > len(payload) {
			t.Fatal("short block")
		}
		dst := make([]byte, u)
		got, err := lz4.UncompressBlock(payload[off:off+n], dst)
		if err != nil || got != u {
			t.Fatalf("decompress %d %v", got, err)
		}
		out = append(out, dst...)
		off += n
	}
	return out
}
