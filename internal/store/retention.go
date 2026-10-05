package store

import "context"

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
