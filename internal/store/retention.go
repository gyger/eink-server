package store

import "context"

// EventRetention bounds the age of events and status samples; EventLimit
// additionally caps the number of retained events.
const EventRetention = "-7 days"
const EventLimit = 10000

// FrameHistoryLimit retains recent frames for in-flight delivery and touch
// correlation. The latest desired and currently displayed frames are preserved.
const FrameHistoryLimit = 100

func (s *Store) PruneFrameHistory(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	obsolete := `SELECT a.id FROM assignments a JOIN devices d ON d.uuid=a.device_uuid
WHERE a.id < COALESCE((SELECT id FROM assignments WHERE device_uuid=a.device_uuid ORDER BY id DESC LIMIT 1 OFFSET ?),0)
AND a.frame_id != d.display_state`
	if _, err := tx.ExecContext(ctx, `DELETE FROM assignment_interactions WHERE assignment_id IN (`+obsolete+`)`, FrameHistoryLimit-1); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM assignments WHERE id IN (`+obsolete+`)`, FrameHistoryLimit-1); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM images WHERE NOT EXISTS (SELECT 1 FROM assignments WHERE image_id=images.id)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM widget_event_consumptions WHERE NOT EXISTS
(SELECT 1 FROM assignments WHERE device_uuid=widget_event_consumptions.device_uuid AND frame_id=widget_event_consumptions.frame_id)`); err != nil {
		return err
	}
	return tx.Commit()
}

// PruneEventHistory removes events and status samples older than
// EventRetention and events beyond the newest EventLimit.
func (s *Store) PruneEventHistory(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM events WHERE created_at < datetime('now',?) OR id <= (SELECT COALESCE(MAX(id),0)-? FROM events)`, EventRetention, EventLimit); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM status_samples WHERE created_at < datetime('now',?)`, EventRetention)
	return err
}

// PruneHistory runs all periodic retention jobs.
func (s *Store) PruneHistory(ctx context.Context) error {
	if err := s.PruneFrameHistory(ctx); err != nil {
		return err
	}
	return s.PruneEventHistory(ctx)
}
