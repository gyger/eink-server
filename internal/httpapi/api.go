package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"eink-server/internal/design"
	"eink-server/internal/events"
	"eink-server/internal/imageproc"
	"eink-server/internal/store"
)

type Connections interface {
	IsConnected(string) bool
	Notify(string)
}
type API struct {
	Store       *store.Store
	Hub         *events.Hub
	Connections Connections
	Designs     *design.Service
	Log         *slog.Logger
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", a.health)
	mux.HandleFunc("GET /api/v1/devices", a.devices)
	mux.HandleFunc("GET /api/v1/devices/{uuid}", a.device)
	mux.HandleFunc("PATCH /api/v1/devices/{uuid}", a.patchDevice)
	mux.HandleFunc("PUT /api/v1/devices/{uuid}/image", a.putImage)
	mux.HandleFunc("PUT /api/v1/devices/{uuid}/design", a.assignDesign)
	mux.HandleFunc("DELETE /api/v1/devices/{uuid}/design", a.clearDesign)
	mux.HandleFunc("GET /api/v1/designs", a.listDesigns)
	mux.HandleFunc("GET /api/v1/designs/{id}", a.getDesign)
	mux.HandleFunc("PUT /api/v1/designs/{name}", a.putDesign)
	mux.HandleFunc("DELETE /api/v1/designs/{name}", a.deleteDesign)
	mux.HandleFunc("POST /api/v1/designs:reload", a.reloadDesigns)
	mux.HandleFunc("GET /api/v1/actions", a.listActions)
	mux.HandleFunc("PUT /api/v1/actions/{name}", a.putAction)
	mux.HandleFunc("DELETE /api/v1/actions/{name}", a.deleteAction)
	mux.HandleFunc("GET /api/v1/devices/{uuid}/image", a.preview)
	mux.HandleFunc("POST /api/v1/images:broadcast", a.broadcast)
	mux.HandleFunc("GET /api/v1/events", a.eventHistory)
	mux.HandleFunc("GET /api/v1/events/stream", a.eventStream)
	mux.HandleFunc("POST /login", a.login)
	mux.HandleFunc("GET /api/device/", a.legacyDevices)
	mux.HandleFunc("PUT /backend/{uuid}", a.legacyUpload)
	mux.HandleFunc("GET /", a.index)
	return recoverMiddleware(a.Log, mux)
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "time": time.Now().UTC()})
}

type deviceView struct {
	store.Device
	Connected bool `json:"connected"`
	Online    bool `json:"online"`
}

func (a *API) view(d store.Device) deviceView {
	connected := a.Connections.IsConnected(d.UUID)
	return deviceView{Device: d, Connected: connected, Online: connected || time.Since(d.LastSeen) < 7*time.Minute}
}

func (a *API) devices(w http.ResponseWriter, r *http.Request) {
	items, err := a.Store.ListDevices(r.Context())
	if err != nil {
		problem(w, 500, "storage_error", err.Error())
		return
	}
	out := make([]deviceView, 0, len(items))
	for _, d := range items {
		out = append(out, a.view(d))
	}
	writeJSON(w, 200, out)
}
func (a *API) device(w http.ResponseWriter, r *http.Request) {
	d, err := a.Store.GetDevice(r.Context(), r.PathValue("uuid"))
	if errors.Is(err, sql.ErrNoRows) {
		problem(w, 404, "device_not_found", "unknown device")
		return
	}
	if err != nil {
		problem(w, 500, "storage_error", err.Error())
		return
	}
	writeJSON(w, 200, a.view(d))
}

func (a *API) patchDevice(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	d, err := a.Store.GetDevice(r.Context(), uuid)
	if err != nil {
		problem(w, 404, "device_not_found", "unknown device")
		return
	}
	var req struct {
		Name          *string             `json:"name"`
		Location      *string             `json:"location"`
		Timezone      *string             `json:"timezone"`
		Locale        *string             `json:"locale"`
		ImageDefaults *imageproc.Settings `json:"image_defaults"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if req.Name != nil {
		d.Name = *req.Name
	}
	if req.Location != nil {
		d.Location = *req.Location
	}
	if req.Timezone != nil {
		d.Timezone = *req.Timezone
	}
	if req.Locale != nil {
		d.Locale = *req.Locale
	}
	if req.ImageDefaults != nil {
		d.Settings = *req.ImageDefaults
	}
	if err := a.Store.UpdateDevice(r.Context(), uuid, d.Name, d.Location, d.Timezone, d.Locale, d.Settings); err != nil {
		problem(w, 400, "invalid_device", err.Error())
		return
	}
	if a.Designs != nil {
		if _, err := a.Designs.Render(r.Context(), uuid, true); err != nil && !errors.Is(err, sql.ErrNoRows) {
			a.Log.Warn("rerendering design after settings update", "uuid", uuid, "error", err)
		}
	}
	a.device(w, r)
}

func (a *API) putImage(w http.ResponseWriter, r *http.Request) {
	a.upload(w, r, []string{r.PathValue("uuid")}, r.Header.Get("Content-Type"), r.Body)
}
func (a *API) broadcast(w http.ResponseWriter, r *http.Request) {
	uuids, err := a.Store.AllUUIDs(r.Context())
	if err != nil {
		problem(w, 500, "storage_error", err.Error())
		return
	}
	if len(uuids) == 0 {
		problem(w, 409, "no_devices", "no enrolled devices")
		return
	}
	a.upload(w, r, uuids, r.Header.Get("Content-Type"), r.Body)
}

func (a *API) upload(w http.ResponseWriter, r *http.Request, uuids []string, contentType string, reader io.Reader) {
	data, err := imageproc.ReadAllLimited(reader)
	if err != nil {
		problem(w, 400, "invalid_image", err.Error())
		return
	}
	if len(data) > imageproc.MaxUploadBytes {
		problem(w, 413, "image_too_large", "image exceeds 20 MiB")
		return
	}
	contentType = strings.Split(contentType, ";")[0]
	if contentType == "image/svg+xml" {
		if len(data) > design.MaxSVGBytes {
			problem(w, 413, "design_too_large", "SVG exceeds 2 MiB")
			return
		}
		if r.URL.RawQuery != "" {
			problem(w, 400, "invalid_settings", "image processing overrides are not supported for SVG designs")
			return
		}
		assignments := make([]store.Assignment, 0, len(uuids))
		for _, uuid := range uuids {
			assignment, err := a.Designs.AssignInline(r.Context(), uuid, data)
			if err != nil {
				problem(w, 400, "invalid_design", err.Error())
				return
			}
			assignments = append(assignments, assignment)
		}
		writeJSON(w, 202, map[string]any{"assignments": assignments})
		return
	}
	if _, err = imageproc.Decode(data, contentType); err != nil {
		problem(w, 400, "invalid_image", err.Error())
		return
	}
	override, err := parseOverride(r)
	if err != nil {
		problem(w, 400, "invalid_settings", err.Error())
		return
	}
	for _, uuid := range uuids {
		_ = a.Store.ClearActiveDesign(r.Context(), uuid)
	}
	assignments, err := a.Store.CreateAssignments(r.Context(), uuids, contentType, data, override)
	if errors.Is(err, sql.ErrNoRows) {
		problem(w, 404, "device_not_found", "unknown device")
		return
	}
	if err != nil {
		problem(w, 400, "queue_failed", err.Error())
		return
	}
	for _, assignment := range assignments {
		a.emit(r.Context(), assignment.DeviceUUID, "image.queued", map[string]any{"assignment_id": assignment.ID, "frame_id": assignment.FrameID})
		a.Connections.Notify(assignment.DeviceUUID)
	}
	writeJSON(w, 202, map[string]any{"assignments": assignments})
}

func parseOverride(r *http.Request) (imageproc.Override, error) {
	q := r.URL.Query()
	var o imageproc.Override
	if v := q.Get("fit"); v != "" {
		o.Fit = &v
	}
	if v := q.Get("background"); v != "" {
		o.Background = &v
	}
	if v := q.Get("dither"); v != "" {
		o.Dither = &v
	}
	if v := q.Get("rendering"); v != "" {
		o.Rendering = &v
	}
	if v := q.Get("rotation"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil {
			return o, e
		}
		o.Rotation = &n
	}
	if v := q.Get("invert"); v != "" {
		b, e := strconv.ParseBool(v)
		if e != nil {
			return o, e
		}
		o.Invert = &b
	}
	_, err := o.Apply(imageproc.Defaults())
	return o, err
}

func (a *API) preview(w http.ResponseWriter, r *http.Request) {
	data, typ, err := a.Store.DesiredPreview(r.Context(), r.PathValue("uuid"))
	if errors.Is(err, sql.ErrNoRows) {
		problem(w, 404, "image_not_found", "device has no desired image")
		return
	}
	if err != nil {
		problem(w, 500, "preview_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", typ)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

func (a *API) eventHistory(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after_id"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := a.Store.Events(r.Context(), after, limit)
	if err != nil {
		problem(w, 500, "storage_error", err.Error())
		return
	}
	writeJSON(w, 200, items)
}
func (a *API) eventStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		problem(w, 500, "stream_unsupported", "streaming unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch, cancel := a.Hub.Subscribe()
	defer cancel()
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case e := <-ch:
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Type, b)
			flusher.Flush()
		}
	}
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "VServer", Value: "compat", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/", http.StatusFound)
}
func (a *API) legacyDevices(w http.ResponseWriter, r *http.Request) {
	items, err := a.Store.ListDevices(r.Context())
	if err != nil {
		problem(w, 500, "storage_error", err.Error())
		return
	}
	out := make([]any, 0, len(items))
	for _, d := range items {
		name := d.Name
		if name == "" {
			name = d.UUID[:12]
		}
		out = append(out, map[string]any{"Uuid": d.UUID, "Options": map[string]string{"Allowed": "true", "Revision": name}, "Status": map[string]any{"Battery": d.Battery, "Temperature": d.Temperature, "Humidity": d.Humidity}, "Displays": []map[string]uint32{{"Width": d.Width, "Height": d.Height}}})
	}
	writeJSON(w, 200, out)
}
func (a *API) legacyUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, imageproc.MaxUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(imageproc.MaxUploadBytes); err != nil {
		problem(w, 400, "invalid_multipart", err.Error())
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, h, err := r.FormFile("image")
	if err != nil {
		problem(w, 400, "missing_image", "multipart field image is required")
		return
	}
	defer file.Close()
	typ := h.Header.Get("Content-Type")
	if typ == "" {
		typ = contentTypeFromHeader(h)
	}
	a.upload(w, r, []string{r.PathValue("uuid")}, typ, file)
}
func contentTypeFromHeader(h *multipart.FileHeader) string {
	if strings.HasSuffix(strings.ToLower(h.Filename), ".png") {
		return "image/png"
	}
	return "image/jpeg"
}

type designView struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	Source     string        `json:"source"`
	PageID     string        `json:"page_id,omitempty"`
	Actions    []design.Rect `json:"actions"`
	Regions    []design.Rect `json:"regions"`
	DependsOn  []string      `json:"depends_on"`
	Unresolved []string      `json:"unresolved_actions,omitempty"`
}

func (a *API) designView(ctx context.Context, d store.Design) (designView, error) {
	meta, err := a.Designs.Metadata(ctx, d)
	if err != nil {
		return designView{}, err
	}
	view := designView{ID: d.ID, Name: d.Name, Source: d.Source, PageID: meta.PageID, Actions: meta.Actions, Regions: meta.Regions, DependsOn: meta.Dependencies}
	if view.Actions == nil {
		view.Actions = []design.Rect{}
	}
	if view.Regions == nil {
		view.Regions = []design.Rect{}
	}
	if view.DependsOn == nil {
		view.DependsOn = []string{}
	}
	seen := map[string]bool{}
	for _, region := range meta.Actions {
		if seen[region.Action] {
			continue
		}
		seen[region.Action] = true
		if _, err := a.Store.GetAction(ctx, region.Action); errors.Is(err, sql.ErrNoRows) {
			view.Unresolved = append(view.Unresolved, region.Action)
		}
	}
	return view, nil
}

func (a *API) listDesigns(w http.ResponseWriter, r *http.Request) {
	items, err := a.Designs.Designs(r.Context())
	if err != nil {
		problem(w, 500, "storage_error", err.Error())
		return
	}
	out := make([]designView, 0, len(items))
	for _, item := range items {
		view, err := a.designView(r.Context(), item)
		if err != nil {
			problem(w, 500, "design_error", err.Error())
			return
		}
		out = append(out, view)
	}
	writeJSON(w, 200, out)
}

func (a *API) getDesign(w http.ResponseWriter, r *http.Request) {
	d, err := a.Store.GetDesign(r.Context(), r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		problem(w, 404, "design_not_found", "unknown design")
		return
	}
	if err != nil {
		problem(w, 500, "storage_error", err.Error())
		return
	}
	view, err := a.designView(r.Context(), d)
	if err != nil {
		problem(w, 500, "design_error", err.Error())
		return
	}
	writeJSON(w, 200, view)
}

func (a *API) putDesign(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, design.MaxSVGBytes+1))
	if err != nil {
		problem(w, 400, "invalid_design", err.Error())
		return
	}
	if len(data) > design.MaxSVGBytes {
		problem(w, 413, "design_too_large", "SVG exceeds 2 MiB")
		return
	}
	d, err := a.Designs.PutDatabaseDesign(r.Context(), r.PathValue("name"), data)
	if err != nil {
		problem(w, 400, "invalid_design", err.Error())
		return
	}
	view, _ := a.designView(r.Context(), d)
	writeJSON(w, 200, view)
}

func (a *API) deleteDesign(w http.ResponseWriter, r *http.Request) {
	if err := a.Designs.DeleteDatabaseDesign(r.Context(), r.PathValue("name")); errors.Is(err, sql.ErrNoRows) {
		problem(w, 404, "design_not_found", "unknown database design")
		return
	} else if err != nil {
		problem(w, 500, "storage_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) reloadDesigns(w http.ResponseWriter, r *http.Request) {
	if err := a.Designs.Reload(r.Context()); err != nil {
		problem(w, 500, "reload_failed", err.Error())
		return
	}
	a.listDesigns(w, r)
}

func (a *API) assignDesign(w http.ResponseWriter, r *http.Request) {
	var request struct {
		DesignID string `json:"design_id"`
	}
	if decodeJSON(w, r, &request) != nil {
		return
	}
	assignment, err := a.Designs.Assign(r.Context(), r.PathValue("uuid"), request.DesignID)
	if errors.Is(err, sql.ErrNoRows) {
		problem(w, 404, "not_found", "unknown device or design")
		return
	}
	if err != nil {
		problem(w, 400, "assignment_failed", err.Error())
		return
	}
	writeJSON(w, 202, assignment)
}

func (a *API) clearDesign(w http.ResponseWriter, r *http.Request) {
	if err := a.Designs.Clear(r.Context(), r.PathValue("uuid")); err != nil {
		problem(w, 500, "storage_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func redactAction(a store.Action) store.Action {
	if len(a.Headers) != 0 {
		headers := make(map[string]string, len(a.Headers))
		for name := range a.Headers {
			headers[name] = "********"
		}
		a.Headers = headers
	}
	return a
}

func (a *API) listActions(w http.ResponseWriter, r *http.Request) {
	items, err := a.Store.Actions(r.Context())
	if err != nil {
		problem(w, 500, "storage_error", err.Error())
		return
	}
	for i := range items {
		items[i] = redactAction(items[i])
	}
	writeJSON(w, 200, items)
}

func (a *API) putAction(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`).MatchString(name) {
		problem(w, 400, "invalid_action", "invalid action name")
		return
	}
	if existing, err := a.Store.GetAction(r.Context(), name); err == nil && existing.Source == "config" {
		problem(w, 409, "config_managed", "action is managed by TOML configuration")
		return
	}
	var request struct {
		Type      string            `json:"type"`
		URL       string            `json:"url"`
		Headers   map[string]string `json:"headers"`
		TimeoutMS int               `json:"timeout_ms"`
	}
	if decodeJSON(w, r, &request) != nil {
		return
	}
	if request.Type == "" {
		request.Type = "webhook"
	}
	if request.Type != "webhook" {
		problem(w, 400, "invalid_action", "only webhook actions are supported")
		return
	}
	u, err := url.Parse(request.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		problem(w, 400, "invalid_action", "URL must use HTTP or HTTPS")
		return
	}
	if request.TimeoutMS == 0 {
		request.TimeoutMS = 5000
	}
	if request.TimeoutMS < 1 || request.TimeoutMS > 30000 {
		problem(w, 400, "invalid_action", "timeout_ms must be between 1 and 30000")
		return
	}
	for header, value := range request.Headers {
		if strings.ContainsAny(header+value, "\r\n") {
			problem(w, 400, "invalid_action", "invalid header")
			return
		}
	}
	item := store.Action{Name: name, Source: "db", Kind: request.Type, URL: request.URL, Headers: request.Headers, TimeoutMS: request.TimeoutMS}
	if err := a.Store.PutAction(r.Context(), item); err != nil {
		problem(w, 500, "storage_error", err.Error())
		return
	}
	item, _ = a.Store.GetAction(r.Context(), name)
	writeJSON(w, 200, redactAction(item))
}

func (a *API) deleteAction(w http.ResponseWriter, r *http.Request) {
	if err := a.Store.DeleteAction(r.Context(), r.PathValue("name"), true); errors.Is(err, sql.ErrNoRows) {
		problem(w, 404, "action_not_found", "unknown dynamic action")
		return
	} else if err != nil {
		problem(w, 500, "storage_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) emit(ctx context.Context, uuid, typ string, data any) {
	e, err := a.Store.AddEvent(ctx, uuid, typ, data)
	if err == nil {
		a.Hub.Publish(e)
	}
}
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		problem(w, 400, "invalid_json", err.Error())
		return err
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func recoverMiddleware(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Error("http panic", "panic", v)
				problem(w, 500, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
