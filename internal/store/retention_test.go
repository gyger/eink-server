package store

import (
	"context"
	"testing"

	"eink-server/internal/imageproc"
	"eink-server/internal/pv3"
)

func TestPruneFrameHistoryPreservesDesiredDisplayedAndSharedImages(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir() + "/retention.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	uuid, other := "00112233-4455-6677-8899-aabbccddeeff", "10112233-4455-6677-8899-aabbccddeeff"
	for _, id := range []string{uuid, other} {
		if _, err := s.UpsertStatus(ctx, pv3.Status{UUID: id, Width: 4, Height: 2}); err != nil {
			t.Fatal(err)
		}
	}
	shared, err := s.CreateAssignments(ctx, []string{uuid, other}, "image/png", tinyPNG(t), imageproc.Override{})
	if err != nil {
		t.Fatal(err)
	}
	var frames []Assignment
	for range FrameHistoryLimit + 2 {
		a, err := s.QueueDesignFrame(ctx, uuid, "inline:test", "", tinyPNG(t), []InteractionRect{{Action: "tap", Width: 2, Height: 2}})
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, a)
		if _, err := s.ClaimWidgetEvent(ctx, uuid, a.FrameID, "calendar:main"); err != nil {
			t.Fatal(err)
		}
	}
	// Keep a displayed frame older than the history window, including its touch map.
	if _, err := s.UpsertStatus(ctx, pv3.Status{UUID: uuid, Width: 4, Height: 2, DisplayState: frames[0].FrameID}); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneFrameHistory(ctx); err != nil {
		t.Fatal(err)
	}
	for _, a := range []Assignment{frames[0], frames[len(frames)-1]} {
		if _, err := s.InteractionForFrame(ctx, uuid, a.FrameID); err != nil {
			t.Fatalf("retained touch map: %v", err)
		}
	}
	if _, err := s.InteractionForFrame(ctx, uuid, frames[1].FrameID); !IsNotFound(err) {
		t.Fatalf("obsolete touch map: %v", err)
	}
	p, err := s.Pending(ctx, uuid)
	if err != nil || p.ID != frames[len(frames)-1].ID {
		t.Fatalf("desired frame=%+v err=%v", p, err)
	}
	p, err = s.Pending(ctx, other)
	if err != nil || p.ID != shared[1].ID || len(p.Source) == 0 {
		t.Fatalf("shared image=%+v err=%v", p, err)
	}
	for table, want := range map[string]int{"assignments": FrameHistoryLimit + 2, "images": FrameHistoryLimit + 2, "assignment_interactions": FrameHistoryLimit + 1, "widget_event_consumptions": FrameHistoryLimit + 1} {
		var count int
		if err := s.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count=%d want=%d err=%v", table, count, want, err)
		}
	}
	if err := s.PruneFrameHistory(ctx); err != nil {
		t.Fatalf("second cleanup: %v", err)
	}
}

func TestPruneEventHistoryKeepsRecentEvents(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir() + "/events.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.DB.Exec(`INSERT INTO events(device_uuid,type,data_json,created_at) VALUES('', 'old', '{}', '2000-01-01T00:00:00Z');
INSERT INTO status_samples(device_uuid,status_json,created_at) VALUES('x','{}','2000-01-01T00:00:00Z'), ('x','{}',?)`, nowString()); err != nil {
		t.Fatal(err)
	}
	for i := range EventLimit + 5 {
		if _, err := s.AddEvent(ctx, "", "test", i); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PruneEventHistory(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count); err != nil || count != EventLimit {
		t.Fatalf("events=%d want=%d err=%v", count, EventLimit, err)
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE type='old'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("old events=%d err=%v", count, err)
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM status_samples`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("samples=%d err=%v", count, err)
	}
}
