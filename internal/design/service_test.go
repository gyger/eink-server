package design

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"eink-server/internal/action"
	"eink-server/internal/events"
	"eink-server/internal/pv3"
	"eink-server/internal/store"
)

type fakeNotifier struct{ uuids []string }

func (f *fakeNotifier) Notify(uuid string) { f.uuids = append(f.uuids, uuid) }

func TestDynamicRenderAndFrameCorrelatedAction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := store.Open(t.TempDir() + "/design.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	uuid := "00112233-4455-6677-8899-aabbccddeeff"
	status := pv3.Status{UUID: uuid, Width: 100, Height: 50, Temperature: 22, Humidity: 41, Fields: map[uint32]uint32{}}
	if _, err := s.UpsertStatus(ctx, status); err != nil {
		t.Fatal(err)
	}
	called := make(chan action.Invocation, 1)
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in action.Invocation
		if err := jsonDecoder(r.Body, &in); err != nil {
			t.Error(err)
		}
		called <- in
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()
	if err := s.PutAction(ctx, store.Action{Name: "tap", Source: "db", Kind: "webhook", URL: webhook.URL, TimeoutMS: 1000}); err != nil {
		t.Fatal(err)
	}
	hub := events.New()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	runner := action.New(ctx, s, hub, log)
	notify := &fakeNotifier{}
	service := &Service{Store: s, Hub: hub, Log: log, Actions: runner, Notifier: notify, SystemName: "Test", DesignDirectory: t.TempDir()}
	if err := service.Init(ctx); err != nil {
		t.Fatal(err)
	}
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 50"><rect width="100" height="50" fill="white"/><text x="2" y="10" data-value="${device.humidity}%">--</text><rect x="0" y="0" width="25" height="25" fill="none" data-action="tap"/></svg>`)
	assignment, err := service.AssignInline(ctx, uuid, svg)
	if err != nil {
		t.Fatal(err)
	}
	if assignment.ID == 0 || len(notify.uuids) != 1 {
		t.Fatalf("assignment=%+v notify=%v", assignment, notify.uuids)
	}
	if noChange, err := service.Render(ctx, uuid, false); err != nil || noChange.ID != 0 {
		t.Fatalf("no-change=%+v err=%v", noChange, err)
	}
	service.Touch(ctx, uuid, assignment.FrameID, 10, 10, 89, 39)
	select {
	case in := <-called:
		if in.Action != "tap" || in.FrameID != assignment.FrameID {
			t.Fatalf("invocation=%+v", in)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook not called")
	}
	status.Humidity = 42
	if _, err := s.UpsertStatus(ctx, status); err != nil {
		t.Fatal(err)
	}
	changed, err := service.Render(ctx, uuid, false)
	if err != nil || changed.ID == 0 {
		t.Fatalf("changed=%+v err=%v", changed, err)
	}
	newUUID := "10112233-4455-6677-8899-aabbccddeeff"
	status.UUID = newUUID
	if _, err := s.UpsertStatus(ctx, status); err != nil {
		t.Fatal(err)
	}
	service.DefaultDesign = "builtin:status"
	service.DeviceEnrolled(ctx, newUUID)
	if active, err := s.ActiveDesign(ctx, newUUID); err != nil || active.DesignID != "builtin:status" {
		t.Fatalf("default active=%+v err=%v", active, err)
	}
}

func TestCalendarWidgetTapUpdatesStateAndRendersOncePerFrame(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := store.Open(t.TempDir() + "/design.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	uuid := "00112233-4455-6677-8899-aabbccddeeff"
	if _, err := s.UpsertStatus(ctx, pv3.Status{UUID: uuid, Width: 700, Height: 600, Fields: map[uint32]uint32{}}); err != nil {
		t.Fatal(err)
	}
	hub := events.New()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	notify := &fakeNotifier{}
	service := &Service{Store: s, Hub: hub, Log: log, Actions: action.New(ctx, s, hub, log), Notifier: notify, SystemName: "Test", DesignDirectory: t.TempDir(), Now: func() time.Time { return time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC) }}
	if err := service.Init(ctx); err != nil {
		t.Fatal(err)
	}
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 700 600"><g id="main" data-widget="calendar" data-navigation="true" data-x="0" data-y="0" data-width="700" data-height="600"/></svg>`)
	assignment, err := service.AssignInline(ctx, uuid, svg)
	if err != nil {
		t.Fatal(err)
	}
	service.Touch(ctx, uuid, assignment.FrameID, 650, 30, 49, 569)
	raw, err := s.GetWidgetState(ctx, uuid, "inline:"+uuid, "calendar", "main")
	if err != nil {
		t.Fatal(err)
	}
	var state calendarState
	if err := json.Unmarshal(raw, &state); err != nil || state.MonthOffset != 1 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if len(notify.uuids) != 2 {
		t.Fatalf("notifications=%v", notify.uuids)
	}
	service.Touch(ctx, uuid, assignment.FrameID, 650, 30, 49, 569)
	if len(notify.uuids) != 2 {
		t.Fatalf("duplicate tap rendered: notifications=%v", notify.uuids)
	}
}

func TestValuesUseDeviceTimezoneAcrossDST(t *testing.T) {
	service := &Service{SystemName: "Test"}
	device := store.Device{UUID: "00112233-4455-6677-8899-aabbccddeeff", Timezone: "Europe/Berlin", Locale: "de-DE"}
	before := service.valuesAt(device, time.Date(2026, 3, 29, 0, 59, 0, 0, time.UTC))
	after := service.valuesAt(device, time.Date(2026, 3, 29, 1, 1, 0, 0, time.UTC))
	if before["system.time"] != "01:59" || after["system.time"] != "03:01" || after["system.date"] != "2026-03-29" || after["system.locale"] != "de-DE" {
		t.Fatalf("before=%v after=%v", before, after)
	}
}

func jsonDecoder(r io.Reader, dst any) error {
	return json.NewDecoder(r).Decode(dst)
}
