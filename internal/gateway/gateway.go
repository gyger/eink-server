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
	"time"

	"joantablet/server/internal/events"
	"joantablet/server/internal/pv3"
	"joantablet/server/internal/render"
	"joantablet/server/internal/store"
)

type Gateway struct {
	Store       *store.Store
	Hub         *events.Hub
	Log         *slog.Logger
	Renderer    render.Renderer
	mu          sync.RWMutex
	connections map[string]*session
	listener    net.Listener
}

type session struct {
	conn       net.Conn
	writeMu    sync.Mutex
	deliverMu  sync.Mutex
	statusMu   sync.RWMutex
	status     pv3.Status
	lastSentID int64
	ready      chan struct{}
	readyOnce  sync.Once
}

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
	go func() {
		<-ctx.Done()
		ln.Close()
		g.mu.Lock()
		for _, s := range g.connections {
			s.conn.Close()
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
		go g.deliver(context.Background(), s)
	}
}

func (g *Gateway) handle(ctx context.Context, c net.Conn) {
	defer c.Close()
	remote := c.RemoteAddr().String()
	var uuid string
	var active *session
	defer func() {
		if active != nil {
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
		if uuid == "" {
			uuid = st.UUID
			active = &session{conn: c, status: st, ready: make(chan struct{})}
			g.mu.Lock()
			if old := g.connections[uuid]; old != nil && old != active {
				old.conn.Close()
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
		}
		if assignmentID, delivered, err := g.Store.MarkDelivered(ctx, uuid, st.DisplayState); err != nil {
			g.Log.Warn("marking frame delivered", "uuid", uuid, "error", err)
		} else if delivered {
			g.emit(ctx, uuid, "image.delivered", map[string]any{"assignment_id": assignmentID, "frame_id": st.DisplayState})
		}
		response, err := pv3.StatusResponse(st, st.Kind == 1)
		if err != nil {
			return
		}
		active.statusMu.Lock()
		active.status = st
		active.statusMu.Unlock()
		if err = active.write(response); err != nil {
			return
		}
		active.readyOnce.Do(func() { close(active.ready) })
		g.emit(ctx, uuid, "device.status", map[string]any{"battery": st.Battery, "temperature": st.Temperature, "humidity": st.Humidity, "display_state": st.DisplayState})
		g.deliver(ctx, active)
	}
}

func (s *session) write(data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.conn.Write(data)
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
	active.statusMu.RLock()
	st := active.status
	active.statusMu.RUnlock()
	uuid := st.UUID
	pending, err := g.Store.Pending(ctx, uuid)
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	if err != nil {
		g.Log.Error("loading desired frame", "uuid", uuid, "error", err)
		return
	}
	if active.lastSentID == pending.ID {
		return
	}
	frame, err := g.Renderer.Render(ctx, render.Input{Device: render.Device{UUID: uuid, Width: pending.Width, Height: pending.Height}, ContentType: pending.ContentType, Source: pending.Source, Settings: pending.Settings})
	if err == nil {
		err = g.Store.PrepareSend(ctx, pending.ID, st.Kind)
	}
	if err == nil {
		var wire []byte
		wire, err = pv3.BuildImage(st.UUIDBytes, st.Kind, pending.FrameID, pending.Width, pending.Height, frame.Packed4Bit)
		if err == nil {
			err = active.write(wire)
		}
	}
	_ = g.Store.MarkSent(ctx, pending.ID, err)
	if err != nil {
		g.emit(ctx, uuid, "image.failed", map[string]any{"assignment_id": pending.ID, "error": err.Error()})
		g.Log.Warn("image delivery failed", "uuid", uuid, "error", err)
		return
	}
	active.lastSentID = pending.ID
	g.emit(ctx, uuid, "image.sent", map[string]any{"assignment_id": pending.ID, "frame_id": pending.FrameID})
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
