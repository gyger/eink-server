package store

import (
	"context"
	"testing"

	"joantablet/server/internal/imageproc"
	"joantablet/server/internal/pv3"
)

func TestPersistenceAndAssignment(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/test.db"
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st := pv3.Status{UUID: "00112233-4455-6677-8899-aabbccddeeff", Battery: 88, Temperature: 21, Width: 1024, Height: 758, Firmware: "7.4.4407", Fields: map[uint32]uint32{10: 88}}
	isNew, err := s.UpsertStatus(ctx, st)
	if err != nil || !isNew {
		t.Fatalf("new=%v err=%v", isNew, err)
	}
	as, err := s.CreateAssignments(ctx, []string{st.UUID}, "image/png", tinyPNG(t), imageproc.Override{})
	if err != nil || len(as) != 1 {
		t.Fatalf("assignments=%v err=%v", as, err)
	}
	if err := s.PrepareSend(ctx, as[0].ID, 3); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSent(ctx, as[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	assignmentID, matched, err := s.MarkAcknowledged(ctx, st.UUID, 3)
	if err != nil || !matched || assignmentID != as[0].ID {
		t.Fatalf("acknowledged id=%d matched=%v err=%v", assignmentID, matched, err)
	}
	if _, matched, err := s.MarkAcknowledged(ctx, st.UUID, 3); err != nil || matched {
		t.Fatalf("duplicate acknowledgement matched=%v err=%v", matched, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := s.GetDevice(ctx, st.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Battery != 88 || d.Desired == nil || d.Desired.FrameID != as[0].FrameID || d.Desired.AcknowledgedAt == nil {
		t.Fatalf("device=%+v", d)
	}
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	return []byte{137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, 73, 72, 68, 82, 0, 0, 0, 1, 0, 0, 0, 1, 8, 0, 0, 0, 0, 58, 126, 155, 85, 0, 0, 0, 10, 73, 68, 65, 84, 120, 156, 99, 248, 15, 0, 1, 1, 1, 0, 24, 221, 141, 176, 0, 0, 0, 0, 73, 69, 78, 68, 174, 66, 96, 130}
}
