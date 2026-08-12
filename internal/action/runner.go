package action

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"eink-server/internal/events"
	"eink-server/internal/store"
)

type Invocation struct {
	Action     string    `json:"action"`
	DeviceUUID string    `json:"device_uuid"`
	DesignID   string    `json:"design_id"`
	PageID     string    `json:"page_id,omitempty"`
	FrameID    uint32    `json:"frame_id"`
	ElementID  string    `json:"element_id,omitempty"`
	Region     string    `json:"region,omitempty"`
	X          uint32    `json:"x"`
	Y          uint32    `json:"y"`
	Timestamp  time.Time `json:"timestamp"`
}

type Runner struct {
	Store  *store.Store
	Hub    *events.Hub
	Log    *slog.Logger
	Client *http.Client
	queue  chan Invocation
}

func New(ctx context.Context, s *store.Store, hub *events.Hub, log *slog.Logger) *Runner {
	r := &Runner{Store: s, Hub: hub, Log: log, Client: &http.Client{}, queue: make(chan Invocation, 100)}
	for range 4 {
		go r.worker(ctx)
	}
	return r
}

func (r *Runner) Submit(ctx context.Context, in Invocation) {
	if in.Timestamp.IsZero() {
		in.Timestamp = time.Now().UTC()
	}
	if _, err := r.Store.GetAction(ctx, in.Action); err != nil {
		if err == sql.ErrNoRows {
			r.Log.Info("touch action unresolved", "uuid", in.DeviceUUID, "frame_id", in.FrameID, "action", in.Action)
			r.emit(ctx, in.DeviceUUID, "action.unresolved", in)
			return
		}
		r.Log.Warn("loading touch action", "action", in.Action, "error", err)
		return
	}
	select {
	case r.queue <- in:
	default:
		r.Log.Warn("action queue full", "action", in.Action, "uuid", in.DeviceUUID)
		r.emit(ctx, in.DeviceUUID, "action.failed", map[string]any{"action": in.Action, "frame_id": in.FrameID, "error": "action queue full"})
	}
}

func (r *Runner) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case in := <-r.queue:
			r.execute(ctx, in)
		}
	}
}

func (r *Runner) execute(parent context.Context, in Invocation) {
	a, err := r.Store.GetAction(parent, in.Action)
	if err != nil {
		r.emit(parent, in.DeviceUUID, "action.failed", map[string]any{"action": in.Action, "error": err.Error()})
		return
	}
	r.emit(parent, in.DeviceUUID, "action.started", in)
	timeout := time.Duration(a.TimeoutMS) * time.Millisecond
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.URL, bytes.NewReader(body))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		for name, value := range a.Headers {
			req.Header.Set(name, value)
		}
		var response *http.Response
		response, err = r.Client.Do(req)
		if err == nil {
			response.Body.Close()
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				err = fmt.Errorf("webhook returned %s", response.Status)
			}
		}
	}
	if err != nil {
		r.Log.Warn("touch action failed", "action", in.Action, "uuid", in.DeviceUUID, "error", err)
		r.emit(parent, in.DeviceUUID, "action.failed", map[string]any{"action": in.Action, "frame_id": in.FrameID, "error": err.Error()})
		return
	}
	r.emit(parent, in.DeviceUUID, "action.succeeded", map[string]any{"action": in.Action, "frame_id": in.FrameID})
}

func (r *Runner) emit(ctx context.Context, uuid, typ string, data any) {
	e, err := r.Store.AddEvent(ctx, uuid, typ, data)
	if err == nil {
		r.Hub.Publish(e)
	}
}
