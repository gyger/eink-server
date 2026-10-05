package gateway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"eink-server/internal/events"
	"eink-server/internal/imageproc"
	"eink-server/internal/pv3"
	"eink-server/internal/render"
	"eink-server/internal/store"
	"github.com/pierrec/lz4/v4"
)

type frameRenderer struct{ pixels []byte }

func (r frameRenderer) Render(context.Context, render.Input) (render.Frame, error) {
	return render.Frame{Packed4Bit: r.pixels}, nil
}

type recordingConn struct {
	bytes.Buffer
	closed bool
}

func (c *recordingConn) Close() error                     { c.closed = true; return nil }
func (c *recordingConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *recordingConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *recordingConn) SetDeadline(time.Time) error      { return nil }
func (c *recordingConn) SetReadDeadline(time.Time) error  { return nil }
func (c *recordingConn) SetWriteDeadline(time.Time) error { return nil }

func testGateway(t *testing.T) (*Gateway, pv3.Status) {
	t.Helper()
	s, err := store.Open(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	g := New(s, events.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	g.Renderer = frameRenderer{[]byte{0xff, 0xff, 0xff, 0xff}}
	st := pv3.Status{UUID: "00112233-4455-6677-8899-aabbccddeeff", Width: 4, Height: 2}
	copy(st.UUIDBytes[:], []byte{0, 17, 34, 51, 68, 85, 102, 119, 136, 153, 170, 187, 204, 221, 238, 255})
	if _, err := s.UpsertStatus(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	return g, st
}

func queueFrame(t *testing.T, g *Gateway, st pv3.Status) store.Assignment {
	t.Helper()
	a, err := g.Store.CreateAssignments(context.Background(), []string{st.UUID}, "image/png", []byte{1}, imageproc.Override{})
	if err != nil {
		t.Fatal(err)
	}
	return a[0]
}

func readImage(t *testing.T, c *recordingConn) []byte {
	t.Helper()
	rec, err := pv3.ReadRecord(c)
	if err != nil {
		t.Fatal(err)
	}
	var logical []byte
	for off := 0; off < len(rec.Payload); {
		n := int(binary.LittleEndian.Uint32(rec.Payload[off+8 : off+12]))
		u := int(binary.LittleEndian.Uint32(rec.Payload[off+12 : off+16]))
		dst := make([]byte, u)
		size, err := lz4.UncompressBlock(rec.Payload[off+24:off+24+n], dst)
		if err != nil || size != u {
			t.Fatalf("decompress size=%d err=%v", size, err)
		}
		logical = append(logical, dst...)
		off += 24 + n
	}
	return logical
}

func TestDeliveryRetriesUnconfirmedFrameAndStopsAfterConfirmation(t *testing.T) {
	g, st := testGateway(t)
	ctx := context.Background()
	older := queueFrame(t, g, st)
	latest := queueFrame(t, g, st)
	c := &recordingConn{}
	active := &session{conn: c, status: st, ready: make(chan struct{})}
	close(active.ready)
	g.deliver(ctx, active)
	wire := readImage(t, c)
	if got := binary.LittleEndian.Uint32(wire[36:40]); got != latest.FrameID {
		t.Fatalf("sent frame=%d want=%d (older=%d)", got, latest.FrameID, older.FrameID)
	}
	if c.Len() != 0 {
		t.Fatal("sent more than latest frame")
	}
	g.deliver(ctx, active)
	if c.Len() != 0 {
		t.Fatal("retried before retry interval")
	}
	active.lastAttemptAt = time.Now().Add(-deliveryRetryInterval)
	g.deliver(ctx, active)
	readImage(t, c)
	if active.attempts != 2 {
		t.Fatalf("attempts=%d", active.attempts)
	}
	if _, ok, err := g.Store.MarkDelivered(ctx, st.UUID, latest.FrameID); err != nil || !ok {
		t.Fatalf("delivery ok=%v err=%v", ok, err)
	}
	active.lastAttemptAt = time.Now().Add(-deliveryRetryInterval)
	g.deliver(ctx, active)
	if c.Len() != 0 || c.closed {
		t.Fatal("confirmed frame should neither resend nor disconnect")
	}
}

func TestDeliveryReconnectsAfterRetryLimit(t *testing.T) {
	g, st := testGateway(t)
	ctx := context.Background()
	a := queueFrame(t, g, st)
	c := &recordingConn{}
	active := &session{conn: c, status: st}
	for range maxDeliveryAttempts {
		active.lastAttemptAt = time.Now().Add(-deliveryRetryInterval)
		if !g.deliverLatest(ctx, active) {
			t.Fatal("expected send")
		}
		readImage(t, c)
	}
	active.lastAttemptAt = time.Now().Add(-deliveryRetryInterval)
	if g.deliverLatest(ctx, active) || !c.closed || c.Len() != 0 {
		t.Fatal("retry exhaustion should close connection without another send")
	}
	d, err := g.Store.GetDevice(ctx, st.UUID)
	if err != nil || d.Desired.ID != a.ID || d.Desired.State != "error" || d.Desired.LastError == "" {
		t.Fatalf("device=%+v err=%v", d, err)
	}
	// A new session retries the retained desired frame.
	next := &recordingConn{}
	if !g.deliverLatest(ctx, &session{conn: next, status: st}) {
		t.Fatal("reconnect did not retry")
	}
	readImage(t, next)
}

func TestPartialDeliveryRequiresConfirmedBase(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconfirmed", true: "confirmed"}[confirmed], func(t *testing.T) {
			g, st := testGateway(t)
			ctx := context.Background()
			first := queueFrame(t, g, st)
			c := &recordingConn{}
			active := &session{conn: c, status: st}
			g.deliverLatest(ctx, active)
			readImage(t, c)
			if confirmed {
				active.status.DisplayState = first.FrameID
			}
			g.Renderer = frameRenderer{[]byte{0xff, 0xff, 0xff, 0x01}}
			queueFrame(t, g, st)
			g.deliverLatest(ctx, active)
			wire := readImage(t, c)
			width := binary.LittleEndian.Uint16(wire[64:66])
			height := binary.LittleEndian.Uint16(wire[66:68])
			if confirmed && (width != 2 || height != 1) {
				t.Fatalf("partial dimensions=%dx%d", width, height)
			}
			if !confirmed && (width != 4 || height != 2) {
				t.Fatalf("full dimensions=%dx%d", width, height)
			}
		})
	}
}

type shortWriteConn struct{ recordingConn }

func (c *shortWriteConn) Write(p []byte) (int, error) { return len(p) / 2, nil }

func TestInterruptedWriteClosesConnectionAndRetainsDesiredFrame(t *testing.T) {
	g, st := testGateway(t)
	ctx := context.Background()
	a := queueFrame(t, g, st)
	c := &shortWriteConn{}
	if g.deliverLatest(ctx, &session{conn: c, status: st}) || !c.closed {
		t.Fatal("short write should fail and close the connection")
	}
	p, err := g.Store.Pending(ctx, st.UUID)
	if err != nil || p.ID != a.ID || p.State != "error" || p.LastError != io.ErrShortWrite.Error() {
		t.Fatalf("pending=%+v err=%v", p, err)
	}
	next := &recordingConn{}
	if !g.deliverLatest(ctx, &session{conn: next, status: st}) {
		t.Fatal("reconnect did not retry interrupted frame")
	}
	readImage(t, next)
}

func statusWire(t *testing.T, st pv3.Status) []byte {
	t.Helper()
	p := make([]byte, 52)
	copy(p[4:20], st.UUIDBytes[:])
	for off, v := range map[int]uint32{20: pv3.MessageStatus, 24: 3, 28: 16, 32: st.Width, 36: 39, 40: st.Height, 44: 40} {
		binary.LittleEndian.PutUint32(p[off:off+4], v)
	}
	wire, err := pv3.MarshalRecord(pv3.Record{Type: pv3.PacketApplication, Payload: p})
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func TestSessionRejectsChangedStatusUUID(t *testing.T) {
	g, st := testGateway(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, tablet := net.Pipe()
	defer tablet.Close()
	if err := tablet.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { g.handle(ctx, server); close(done) }()
	if _, err := tablet.Write(statusWire(t, st)); err != nil {
		t.Fatal(err)
	}
	if _, err := pv3.ReadRecord(tablet); err != nil {
		t.Fatal(err)
	}
	other := st
	other.UUIDBytes[0] = 0x10
	if _, err := tablet.Write(statusWire(t, other)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("mismatched UUID did not terminate session")
	}
	if _, err := g.Store.GetDevice(ctx, "10112233-4455-6677-8899-aabbccddeeff"); err != sql.ErrNoRows {
		t.Fatalf("mismatched device enrolled: %v", err)
	}
	if g.IsConnected(st.UUID) {
		t.Fatal("closed session remains connected")
	}
}
