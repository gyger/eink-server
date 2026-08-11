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

	"joantablet/server/internal/events"
	"joantablet/server/internal/pv3"
	"joantablet/server/internal/store"
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
	_, err = s.UpsertStatus(context.Background(), pv3.Status{UUID: uuid, Battery: 75, Temperature: 22, Width: 8, Height: 4, Firmware: "7.4.4407", Fields: map[uint32]uint32{}})
	if err != nil {
		t.Fatal(err)
	}
	c := &fakeConnections{}
	return &API{Store: s, Hub: events.New(), Connections: c, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, s, c
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
	api, _, _ := testAPI(t)
	for _, path := range []string{"/api/v1/devices", "/api/device/"} {
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != 200 {
			t.Fatalf("%s status=%d", path, w.Code)
		}
		var items []map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil || len(items) != 1 {
			t.Fatalf("%s body=%s err=%v", path, w.Body.String(), err)
		}
	}
	w := httptest.NewRecorder()
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
