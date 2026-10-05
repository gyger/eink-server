package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"eink-server/internal/events"
	"eink-server/internal/pv3"
	"eink-server/internal/render"
	"eink-server/internal/store"
)

type Gateway struct {
	Store    *store.Store
	Hub      *events.Hub
	Log      *slog.Logger
	Renderer render.Renderer
	Designs  interface {
		DeviceEnrolled(context.Context, string)
		StatusChanged(context.Context, string)
		Touch(context.Context, string, uint32, uint32, uint32, uint32, uint32)
	}
	mu          sync.RWMutex
	connections map[string]*session
	listener    net.Listener
}

type session struct {
	conn      net.Conn
	closed    atomic.Bool
	writeMu   sync.Mutex
	deliverMu sync.Mutex
	statusMu  sync.RWMutex
	status    pv3.Status
	ready     chan struct{}
	readyOnce sync.Once
	ackMu     sync.Mutex
	nextSeq   uint32
	ackedSeq  uint32

	// Delivery bookkeeping, guarded by deliverMu.
	lastFrameID    uint32
	lastSentSeq    uint32
	attemptID      int64
	attempts       int
	lastAttemptAt  time.Time
	renderFailedID int64
	lastFrame      []byte
	frameWidth     int
	frameHeight    int
}

const tabletWriteTimeout = 10 * time.Second
const deliveryRetryInterval = 15 * time.Second

// maxDeliveryAttempts bounds unconfirmed sends per connection before the
// gateway forces a reconnect. maxTotalDeliveryAttempts bounds them across
// reconnects before the assignment is marked failed.
const maxDeliveryAttempts = 3
const maxTotalDeliveryAttempts = 3 * maxDeliveryAttempts

func New(s *store.Store, h *events.Hub, log *slog.Logger) *Gateway {
	return &Gateway{Store: s, Hub: h, Log: log, Renderer: render.UploadedImage{}, connections: make(map[string]*session)}
}

func (g *Gateway) Serve(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	g.listener = ln
	g.Log.Info("tablet gateway listening", "address", ln.Addr())
	go g.retryDeliveries(ctx)
	go func() {
		<-ctx.Done()
		ln.Close()
		g.mu.Lock()
		for _, s := range g.connections {
			s.close()
		}
		g.mu.Unlock()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go g.handle(ctx, c)
	}
}

func (g *Gateway) IsConnected(uuid string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.connections[uuid]
	return ok
}

func (g *Gateway) Notify(uuid string) {
	g.mu.RLock()
	s := g.connections[uuid]
	g.mu.RUnlock()
	if s != nil {
		g.Log.Debug("image delivery notified", "uuid", uuid)
		go g.deliver(context.Background(), s)
	}
}

func (g *Gateway) retryDeliveries(ctx context.Context) {
	ticker := time.NewTicker(deliveryRetryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.mu.RLock()
			sessions := make([]*session, 0, len(g.connections))
			for _, active := range g.connections {
				sessions = append(sessions, active)
			}
			g.mu.RUnlock()
			for _, active := range sessions {
				go g.deliver(ctx, active)
			}
		}
	}
}

func (g *Gateway) handle(ctx context.Context, c net.Conn) {
	defer c.Close()
	remote := c.RemoteAddr().String()
	var uuid string
	var active *session
	defer func() {
		if active != nil {
			active.closed.Store(true)
			active.readyOnce.Do(func() { close(active.ready) })
		}
		if uuid != "" {
			g.mu.Lock()
			if g.connections[uuid] == active {
				delete(g.connections, uuid)
			}
			g.mu.Unlock()
			g.emit(context.Background(), uuid, "device.disconnected", map[string]any{"remote": remote})
		}
	}()
	for {
		_ = c.SetReadDeadline(time.Now().Add(20 * time.Minute))
		rec, err := pv3.ReadRecord(c)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				g.Log.Warn("tablet record rejected", "remote", remote, "error", err)
			}
			return
		}
		if rec.Type == pv3.PacketBootloader {
			g.Log.Info("bootloader connection ignored", "remote", remote)
			return
		}
		messageType, err := pv3.ApplicationMessageType(rec)
		if err != nil {
			g.Log.Warn("application record ignored", "remote", remote, "error", err)
			continue
		}
		if messageType == pv3.MessageAcknowledgement {
			ack, err := pv3.ParseAcknowledgement(rec)
			if err != nil {
				g.Log.Warn("image acknowledgement rejected", "remote", remote, "error", err)
				continue
			}
			if active == nil || ack.UUID != uuid {
				g.Log.Warn("image acknowledgement UUID mismatch", "remote", remote, "uuid", ack.UUID)
				continue
			}
			assignmentID, matched, err := g.Store.MarkAcknowledged(ctx, uuid, ack.Sequence)
			if err != nil {
				g.Log.Warn("saving image acknowledgement", "uuid", uuid, "sequence", ack.Sequence, "error", err)
			} else if matched {
				active.noteAcknowledged(ack.Sequence)
				g.emit(ctx, uuid, "image.acknowledged", map[string]any{"assignment_id": assignmentID, "sequence": ack.Sequence})
			} else {
				g.Log.Debug("unmatched image acknowledgement", "uuid", uuid, "sequence", ack.Sequence)
			}
			continue
		}
		if messageType == pv3.MessageTouch {
			touch, err := pv3.ParseTouch(rec)
			if err != nil {
				g.Log.Warn("touch record rejected", "remote", remote, "error", err)
				continue
			}
			if active == nil || touch.UUID != uuid {
				g.Log.Warn("touch record UUID mismatch", "remote", remote, "uuid", touch.UUID)
				continue
			}
			active.statusMu.RLock()
			width, height := active.status.Width, active.status.Height
			active.statusMu.RUnlock()
			x, y, err := touch.PhysicalCoordinates(width, height)
			if err != nil {
				g.Log.Warn("touch coordinate rejected", "uuid", uuid, "raw_x", touch.RawX, "raw_y", touch.RawY, "width", width, "height", height, "error", err)
				continue
			}
			g.Log.Info("touch event", "uuid", uuid, "frame_id", touch.FrameID, "x", x, "y", y, "raw_x", touch.RawX, "raw_y", touch.RawY)
			if g.Designs != nil {
				g.Designs.Touch(ctx, uuid, touch.FrameID, x, y, touch.RawX, touch.RawY)
			}
			continue
		}
		if messageType != pv3.MessageStatus {
			g.Log.Warn("unsupported application message", "remote", remote, "message_type", messageType)
			continue
		}
		st, err := pv3.ParseStatus(rec)
		if err != nil {
			g.Log.Warn("status record rejected", "remote", remote, "error", err)
			continue
		}
		if uuid != "" && st.UUID != uuid {
			g.Log.Warn("status UUID mismatch", "remote", remote, "uuid", st.UUID, "expected_uuid", uuid)
			return
		}
		firstStatus := uuid == ""
		if firstStatus {
			uuid = st.UUID
			active = &session{conn: c, status: st, ready: make(chan struct{}), nextSeq: st.Kind + 1}
			g.mu.Lock()
			if old := g.connections[uuid]; old != nil && old != active {
				old.close()
			}
			g.connections[uuid] = active
			g.mu.Unlock()
			g.emit(ctx, uuid, "device.connected", map[string]any{"remote": remote})
		}
		isNew, err := g.Store.UpsertStatus(ctx, st)
		if err != nil {
			g.Log.Error("saving tablet status", "uuid", uuid, "error", err)
			return
		}
		if isNew {
			g.emit(ctx, uuid, "device.enrolled", map[string]any{"width": st.Width, "height": st.Height})
			if g.Designs != nil {
				g.Designs.DeviceEnrolled(ctx, uuid)
			}
		}
		if assignmentID, delivered, err := g.Store.MarkDelivered(ctx, uuid, st.DisplayState); err != nil {
			g.Log.Warn("marking frame delivered", "uuid", uuid, "error", err)
		} else if delivered {
			g.emit(ctx, uuid, "image.delivered", map[string]any{"assignment_id": assignmentID, "frame_id": st.DisplayState})
		}
		if firstStatus {
			if err := g.Store.ReconcileDesired(ctx, uuid, st.DisplayState); err != nil {
				g.Log.Error("reconciling desired frame", "uuid", uuid, "error", err)
				return
			}
		}
		response, err := pv3.StatusResponse(st, st.Kind == 1)
		if err != nil {
			return
		}
		active.statusMu.Lock()
		active.status = st
		active.statusMu.Unlock()
		active.advanceSequence(st.Kind)
		if err = active.write(response); err != nil {
			return
		}
		active.readyOnce.Do(func() { close(active.ready) })
		g.emit(ctx, uuid, "device.status", map[string]any{"battery": st.Battery, "temperature": st.Temperature, "humidity": st.Humidity, "display_state": st.DisplayState})
		if g.Designs != nil {
			g.Designs.StatusChanged(ctx, uuid)
		}
		go g.deliver(context.Background(), active)
	}
}

func (s *session) sequence() uint32 {
	s.ackMu.Lock()
	defer s.ackMu.Unlock()
	if s.nextSeq == 0 {
		s.nextSeq = 1
	}
	sequence := s.nextSeq
	s.nextSeq++
	if s.nextSeq == 0 {
		s.nextSeq = 1
	}
	return sequence
}

func (s *session) advanceSequence(observed uint32) {
	s.ackMu.Lock()
	if s.nextSeq <= observed {
		s.nextSeq = observed + 1
		if s.nextSeq == 0 {
			s.nextSeq = 1
		}
	}
	s.ackMu.Unlock()
}

func (s *session) noteAcknowledged(sequence uint32) {
	s.ackMu.Lock()
	s.ackedSeq = sequence
	s.ackMu.Unlock()
}

func (s *session) acknowledged(sequence uint32) bool {
	s.ackMu.Lock()
	defer s.ackMu.Unlock()
	return sequence != 0 && s.ackedSeq == sequence
}

// close marks the session dead so pending delivery work skips it, then closes
// the connection so the reader loop exits.
func (s *session) close() {
	s.closed.Store(true)
	s.conn.Close()
}

func (s *session) write(data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.conn.SetWriteDeadline(time.Now().Add(tabletWriteTimeout)); err != nil {
		return err
	}
	defer s.conn.SetWriteDeadline(time.Time{})
	n, err := s.conn.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return err
}

func (g *Gateway) deliver(ctx context.Context, active *session) {
	select {
	case <-active.ready:
	case <-ctx.Done():
		return
	}
	active.deliverMu.Lock()
	defer active.deliverMu.Unlock()
	for {
		if !g.deliverLatest(ctx, active) {
			return
		}
	}
}

// deliverLatest sends the newest desired assignment. It returns true after a
// successful send so the caller checks once more for work queued concurrently.
func (g *Gateway) deliverLatest(ctx context.Context, active *session) bool {
	if active.closed.Load() {
		return false
	}
	active.statusMu.RLock()
	st := active.status
	active.statusMu.RUnlock()
	uuid := st.UUID
	pending, err := g.Store.Pending(ctx, uuid)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		g.Log.Error("loading desired frame", "uuid", uuid, "error", err)
		return false
	}
	if active.renderFailedID == pending.ID {
		// Rendering is deterministic for a stored frame; wait for a newer one.
		return false
	}
	if pending.SendAttempts >= maxTotalDeliveryAttempts {
		reason := fmt.Sprintf("delivery not confirmed after %d attempts; waiting for a new frame", pending.SendAttempts)
		if saveErr := g.Store.MarkFailed(ctx, pending.ID, reason); saveErr != nil {
			g.Log.Warn("saving failed delivery", "uuid", uuid, "error", saveErr)
		}
		g.Log.Warn("image delivery abandoned", "uuid", uuid, "assignment_id", pending.ID, "attempts", pending.SendAttempts)
		g.emit(ctx, uuid, "image.failed", map[string]any{"assignment_id": pending.ID, "error": reason, "final": true})
		return false
	}
	if active.attemptID == pending.ID {
		if time.Since(active.lastAttemptAt) < deliveryRetryInterval {
			return false
		}
		if active.attempts >= maxDeliveryAttempts {
			err := fmt.Errorf("delivery not confirmed after %d attempts; reconnecting", active.attempts)
			if saveErr := g.Store.MarkSent(ctx, pending.ID, err); saveErr != nil {
				g.Log.Warn("saving exhausted delivery", "uuid", uuid, "error", saveErr)
			}
			g.emit(ctx, uuid, "image.failed", map[string]any{"assignment_id": pending.ID, "error": err.Error()})
			active.close()
			return false
		}
	} else {
		active.attemptID, active.attempts = pending.ID, 0
	}
	g.Log.Debug("image delivery starting", "uuid", uuid, "assignment_id", pending.ID, "frame_id", pending.FrameID)
	frame, err := g.Renderer.Render(ctx, render.Input{Device: render.Device{UUID: uuid, Width: pending.Width, Height: pending.Height}, ContentType: pending.ContentType, Source: pending.Source, Settings: pending.Settings})
	if err != nil {
		// A render failure says nothing about the connection and would repeat
		// identically, so it neither counts as an attempt nor reconnects.
		active.renderFailedID = pending.ID
		if saveErr := g.Store.MarkSent(ctx, pending.ID, err); saveErr != nil {
			g.Log.Warn("saving image render state", "uuid", uuid, "error", saveErr)
		}
		g.emit(ctx, uuid, "image.failed", map[string]any{"assignment_id": pending.ID, "error": err.Error()})
		g.Log.Warn("image render failed", "uuid", uuid, "assignment_id", pending.ID, "error", err)
		return false
	}
	active.attempts++
	active.lastAttemptAt = time.Now()
	// Partial updates are safe only against a base frame the tablet confirmed,
	// either by echoing its frame ID in a status or by acknowledging its record.
	previous := active.lastFrame
	if active.lastFrameID == 0 || (st.DisplayState != active.lastFrameID && !active.acknowledged(active.lastSentSeq)) {
		previous = nil
	}
	primitive := changedPrimitive(frame.Packed4Bit, previous, pending.Width, pending.Height, active.frameWidth, active.frameHeight)
	finalSequence := active.sequence()
	writeFailed := false
	err = g.Store.PrepareSend(ctx, pending.ID, finalSequence)
	if err == nil {
		var wire []byte
		wire, err = pv3.BuildImagePrimitives(st.UUIDBytes, finalSequence, pending.FrameID, []pv3.ImagePrimitive{primitive})
		if err == nil {
			err = active.write(wire)
			writeFailed = err != nil
		}
	}
	if saveErr := g.Store.MarkSent(ctx, pending.ID, err); saveErr != nil {
		g.Log.Warn("saving image send state", "uuid", uuid, "error", saveErr)
	}
	if err != nil {
		g.emit(ctx, uuid, "image.failed", map[string]any{"assignment_id": pending.ID, "error": err.Error()})
		g.Log.Warn("image delivery failed", "uuid", uuid, "error", err)
		// A failed write may leave a partial record on the stream. Reconnect before retrying.
		if writeFailed {
			active.close()
		}
		return false
	}
	active.lastFrameID = pending.FrameID
	active.lastSentSeq = finalSequence
	active.lastFrame = append(active.lastFrame[:0], frame.Packed4Bit...)
	active.frameWidth, active.frameHeight = pending.Width, pending.Height
	g.emit(ctx, uuid, "image.sent", map[string]any{"assignment_id": pending.ID, "frame_id": pending.FrameID})
	return true
}

func changedPrimitive(current, previous []byte, width, height, previousWidth, previousHeight int) pv3.ImagePrimitive {
	bytesPerRow := width / 2
	if width <= 0 || height <= 0 || width%2 != 0 || len(current) != bytesPerRow*height ||
		previousWidth != width || previousHeight != height || len(previous) != len(current) {
		return pv3.ImagePrimitive{Width: width, Height: height, Pixels: current}
	}
	minX, minY, maxX, maxY := bytesPerRow, height, -1, -1
	for y := 0; y < height; y++ {
		for x := 0; x < bytesPerRow; x++ {
			i := y*bytesPerRow + x
			if current[i] != previous[i] {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX < 0 {
		return pv3.ImagePrimitive{Width: width, Height: height, Pixels: current}
	}
	rowBytes := maxX - minX + 1
	pixels := make([]byte, 0, rowBytes*(maxY-minY+1))
	for y := minY; y <= maxY; y++ {
		start := y*bytesPerRow + minX
		pixels = append(pixels, current[start:start+rowBytes]...)
	}
	return pv3.ImagePrimitive{X: minX * 2, Y: minY, Width: rowBytes * 2, Height: maxY - minY + 1, Pixels: pixels}
}

func (g *Gateway) emit(ctx context.Context, uuid, typ string, data any) {
	e, err := g.Store.AddEvent(ctx, uuid, typ, data)
	if err != nil {
		g.Log.Warn("saving event", "type", typ, "error", err)
		return
	}
	g.Hub.Publish(e)
}

func (g *Gateway) Address() string {
	if g.listener == nil {
		return ""
	}
	return fmt.Sprint(g.listener.Addr())
}
