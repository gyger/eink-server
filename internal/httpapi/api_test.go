package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"eink-server/internal/action"
	"eink-server/internal/design"
	"eink-server/internal/events"
	"eink-server/internal/pv3"
	"eink-server/internal/store"
)

type fakeConnections struct{ notified []string }

func (f *fakeConnections) IsConnected(string) bool { return false }
func (f *fakeConnections) Notify(uuid string)      { f.notified = append(f.notified, uuid) }

func testAPI(t *testing.T) (*API, *store.Store, *fakeConnections) {
	t.Helper()
	s, err := store.Open(t.TempDir() + "/api.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	uuid := "00112233-4455-6677-8899-aabbccddeeff"
	_, err = s.UpsertStatus(context.Background(), pv3.Status{UUID: uuid, Battery: 75, Temperature: 22, Humidity: 41, Width: 8, Height: 4, Firmware: "7.4.4407", Fields: map[uint32]uint32{15: 41}})
	if err != nil {
		t.Fatal(err)
	}
	c := &fakeConnections{}
	hub := events.New()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runner := action.New(ctx, s, hub, log)
	designs := &design.Service{Store: s, Hub: hub, Log: log, Actions: runner, Notifier: c, SystemName: "Test", DesignDirectory: t.TempDir()}
	if err := designs.Init(ctx); err != nil {
		t.Fatal(err)
	}
	return &API{Store: s, Hub: hub, Connections: c, Designs: designs, Log: log}, s, c
}

func TestNativeImageUpload(t *testing.T) {
	api, s, c := testAPI(t)
	var body bytes.Buffer
	img := image.NewGray(image.Rect(0, 0, 8, 4))
	for i := range img.Pix {
		img.Pix[i] = color.Gray{Y: uint8(i * 7)}.Y
	}
	if err := png.Encode(&body, img); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/devices/00112233-4455-6677-8899-aabbccddeeff/image?fit=exact", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	p, err := s.Pending(context.Background(), "00112233-4455-6677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	if p.Settings.Fit != "exact" || len(c.notified) != 1 {
		t.Fatalf("pending=%+v notified=%v", p, c.notified)
	}
}

func TestDeviceAndLegacyShapes(t *testing.T) {
	api, s, _ := testAPI(t)
	uuid := "00112233-4455-6677-8899-aabbccddeeff"
	statusDesign, err := s.GetDesign(context.Background(), "builtin:status")
	if err != nil || s.SetActiveDesign(context.Background(), uuid, statusDesign.ID, statusDesign.SVG) != nil {
		t.Fatalf("activate status design: %v", err)
	}
	patch := httptest.NewRequest(http.MethodPatch, "/api/v1/devices/"+uuid, bytes.NewBufferString(`{"location":"Meeting room"}`))
	patch.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, patch)
	if w.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", w.Code, w.Body.String())
	}
	if d, err := s.GetDevice(context.Background(), uuid); err != nil || d.Location != "Meeting room" {
		t.Fatalf("device=%+v err=%v", d, err)
	}
	for _, path := range []string{"/api/v1/devices", "/api/device/"} {
		w = httptest.NewRecorder()
		api.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != 200 {
			t.Fatalf("%s status=%d", path, w.Code)
		}
		var items []map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil || len(items) != 1 {
			t.Fatalf("%s body=%s err=%v", path, w.Body.String(), err)
		}
		if path == "/api/v1/devices" && (items[0]["humidity"] != float64(41) || items[0]["active_design_id"] != "builtin:status") {
			t.Fatalf("%s device=%v", path, items[0])
		}
	}
	w = httptest.NewRecorder()
	api.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/login", nil))
	if w.Code != http.StatusFound {
		t.Fatalf("login=%d", w.Code)
	}
}

func TestRejectsUnsupportedImage(t *testing.T) {
	api, _, _ := testAPI(t)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/devices/00112233-4455-6677-8899-aabbccddeeff/image", bytes.NewBufferString("not an image"))
	req.Header.Set("Content-Type", "image/gif")
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestSVGDesignAndActionAPIs(t *testing.T) {
	api, s, _ := testAPI(t)
	uuid := "00112233-4455-6677-8899-aabbccddeeff"
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 8 4"><rect width="8" height="4" fill="white" data-action="bell"/></svg>`
	request := httptest.NewRequest(http.MethodPut, "/api/v1/designs/room", bytes.NewBufferString(svg))
	request.Header.Set("Content-Type", "image/svg+xml")
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, request)
	if w.Code != 200 {
		t.Fatalf("put design status=%d body=%s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	api.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/devices/"+uuid+"/design", bytes.NewBufferString(`{"design_id":"db:room"}`)))
	if w.Code != http.StatusAccepted {
		t.Fatalf("assign status=%d body=%s", w.Code, w.Body.String())
	}
	if active, err := s.ActiveDesign(context.Background(), uuid); err != nil || active.DesignID != "db:room" {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	actionBody := `{"type":"webhook","url":"https://example.test/hook","headers":{"Authorization":"secret"},"timeout_ms":1000}`
	w = httptest.NewRecorder()
	api.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/actions/bell", bytes.NewBufferString(actionBody)))
	if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte("secret")) {
		t.Fatalf("action status=%d body=%s", w.Code, w.Body.String())
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPut, "/api/v1/devices/"+uuid+"/image?fit=exact", &encoded)
	request.Header.Set("Content-Type", "image/png")
	w = httptest.NewRecorder()
	api.Handler().ServeHTTP(w, request)
	if w.Code != http.StatusAccepted {
		t.Fatalf("image status=%d body=%s", w.Code, w.Body.String())
	}
	if _, err := s.ActiveDesign(context.Background(), uuid); !store.IsNotFound(err) {
		t.Fatalf("active design survived PNG upload: %v", err)
	}
}
