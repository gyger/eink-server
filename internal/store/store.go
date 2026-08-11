package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"joantablet/server/internal/imageproc"
	"joantablet/server/internal/pv3"
)

type Store struct{ DB *sql.DB }

type Device struct {
	UUID         string             `json:"uuid"`
	Name         string             `json:"name"`
	FirstSeen    time.Time          `json:"first_seen"`
	LastSeen     time.Time          `json:"last_seen"`
	Battery      uint32             `json:"battery"`
	Temperature  int32              `json:"temperature"`
	Width        uint32             `json:"width"`
	Height       uint32             `json:"height"`
	Firmware     string             `json:"firmware"`
	DisplayState uint32             `json:"display_state"`
	Settings     imageproc.Settings `json:"image_defaults"`
	Desired      *Assignment        `json:"desired_frame,omitempty"`
}

type Assignment struct {
	ID             int64      `json:"id"`
	DeviceUUID     string     `json:"device_uuid"`
	FrameID        uint32     `json:"frame_id"`
	State          string     `json:"state"`
	QueuedAt       time.Time  `json:"queued_at"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
}

type Pending struct {
	Assignment
	Width, Height int
	ContentType   string
	Source        []byte
	Settings      imageproc.Settings
}

type Event struct {
	ID         int64           `json:"id"`
	DeviceUUID string          `json:"device_uuid,omitempty"`
	Type       string          `json:"type"`
	Data       json.RawMessage `json:"data"`
	CreatedAt  time.Time       `json:"created_at"`
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL);
INSERT INTO schema_version(version) SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM schema_version);
CREATE TABLE IF NOT EXISTS devices (
 uuid TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', first_seen TEXT NOT NULL, last_seen TEXT NOT NULL,
 battery INTEGER NOT NULL DEFAULT 0, temperature INTEGER NOT NULL DEFAULT 0, width INTEGER NOT NULL DEFAULT 0,
 height INTEGER NOT NULL DEFAULT 0, firmware TEXT NOT NULL DEFAULT '', display_state INTEGER NOT NULL DEFAULT 0,
 status_json TEXT NOT NULL DEFAULT '{}', settings_json TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS images (
 id INTEGER PRIMARY KEY AUTOINCREMENT, content_type TEXT NOT NULL, source BLOB NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS assignments (
 id INTEGER PRIMARY KEY AUTOINCREMENT, device_uuid TEXT NOT NULL REFERENCES devices(uuid), image_id INTEGER NOT NULL REFERENCES images(id),
 frame_id INTEGER NOT NULL, settings_json TEXT NOT NULL, state TEXT NOT NULL, queued_at TEXT NOT NULL,
 delivered_at TEXT, acknowledged_at TEXT, sent_sequence INTEGER, last_error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS assignments_device_id ON assignments(device_uuid,id DESC);
CREATE TABLE IF NOT EXISTS events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, device_uuid TEXT NOT NULL DEFAULT '', type TEXT NOT NULL, data_json TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS events_created ON events(created_at);
CREATE TABLE IF NOT EXISTS status_samples (
 id INTEGER PRIMARY KEY AUTOINCREMENT, device_uuid TEXT NOT NULL, status_json TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS samples_device_created ON status_samples(device_uuid,created_at);
`
	_, err := s.DB.ExecContext(ctx, schema)
	return err
}

func nowString() string            { return time.Now().UTC().Format(time.RFC3339Nano) }
func parseTime(v string) time.Time { t, _ := time.Parse(time.RFC3339Nano, v); return t }

func (s *Store) UpsertStatus(ctx context.Context, st pv3.Status) (bool, error) {
	now := nowString()
	raw, _ := json.Marshal(st.Fields)
	defaults := imageproc.Defaults().JSON()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO devices(uuid,name,first_seen,last_seen,battery,temperature,width,height,firmware,display_state,status_json,settings_json)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(uuid) DO UPDATE SET last_seen=excluded.last_seen,battery=excluded.battery,
temperature=excluded.temperature,width=excluded.width,height=excluded.height,firmware=excluded.firmware,display_state=excluded.display_state,status_json=excluded.status_json`,
		st.UUID, "", now, now, st.Battery, st.Temperature, st.Width, st.Height, st.Firmware, st.DisplayState, string(raw), defaults)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	// SQLite reports an affected row for inserts and updates; first_seen equality identifies a new row.
	var first string
	if err := s.DB.QueryRowContext(ctx, "SELECT first_seen FROM devices WHERE uuid=?", st.UUID).Scan(&first); err != nil {
		return false, err
	}
	isNew := first == now
	_ = n
	return isNew, s.sampleStatus(ctx, st, string(raw), now)
}

func (s *Store) MarkDelivered(ctx context.Context, uuid string, frameID uint32) (int64, bool, error) {
	if frameID == 0 {
		return 0, false, nil
	}
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM assignments WHERE device_uuid=? AND frame_id=? AND state!='delivered' ORDER BY id DESC LIMIT 1`, uuid, frameID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE assignments SET state='delivered',delivered_at=?,last_error='' WHERE id=?`, nowString(), id)
	return id, err == nil, err
}

func (s *Store) sampleStatus(ctx context.Context, st pv3.Status, raw, now string) error {
	var previous string
	err := s.DB.QueryRowContext(ctx, `SELECT status_json FROM status_samples WHERE device_uuid=? ORDER BY id DESC LIMIT 1`, st.UUID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var last string
	_ = s.DB.QueryRowContext(ctx, `SELECT created_at FROM status_samples WHERE device_uuid=? ORDER BY id DESC LIMIT 1`, st.UUID).Scan(&last)
	lt := parseTime(last)
	if previous != raw || lt.IsZero() || time.Since(lt) >= 15*time.Minute {
		_, err = s.DB.ExecContext(ctx, `INSERT INTO status_samples(device_uuid,status_json,created_at) VALUES(?,?,?)`, st.UUID, raw, now)
	}
	return err
}

func (s *Store) ListDevices(ctx context.Context) ([]Device, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT uuid,name,first_seen,last_seen,battery,temperature,width,height,firmware,display_state,settings_json FROM devices ORDER BY name,uuid`)
	if err != nil {
		return nil, err
	}
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Desired, _ = s.latestAssignment(ctx, out[i].UUID)
	}
	return out, nil
}

type scanner interface{ Scan(...any) error }

func scanDevice(row scanner) (Device, error) {
	var d Device
	var first, last, settings string
	err := row.Scan(&d.UUID, &d.Name, &first, &last, &d.Battery, &d.Temperature, &d.Width, &d.Height, &d.Firmware, &d.DisplayState, &settings)
	if err != nil {
		return d, err
	}
	d.FirstSeen = parseTime(first)
	d.LastSeen = parseTime(last)
	d.Settings, err = imageproc.ParseSettings(settings)
	return d, err
}

func (s *Store) GetDevice(ctx context.Context, uuid string) (Device, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT uuid,name,first_seen,last_seen,battery,temperature,width,height,firmware,display_state,settings_json FROM devices WHERE uuid=?`, uuid)
	d, err := scanDevice(row)
	if err == nil {
		d.Desired, _ = s.latestAssignment(ctx, uuid)
	}
	return d, err
}

func (s *Store) UpdateDevice(ctx context.Context, uuid, name string, settings imageproc.Settings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE devices SET name=?,settings_json=? WHERE uuid=?`, name, settings.JSON(), uuid)
	if err == nil {
		n, _ := res.RowsAffected()
		if n == 0 {
			return sql.ErrNoRows
		}
	}
	return err
}

func (s *Store) CreateAssignments(ctx context.Context, uuids []string, contentType string, source []byte, override imageproc.Override) ([]Assignment, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := nowString()
	res, err := tx.ExecContext(ctx, `INSERT INTO images(content_type,source,created_at) VALUES(?,?,?)`, contentType, source, now)
	if err != nil {
		return nil, err
	}
	imageID, _ := res.LastInsertId()
	out := make([]Assignment, 0, len(uuids))
	for _, uuid := range uuids {
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT settings_json FROM devices WHERE uuid=?`, uuid).Scan(&raw); err != nil {
			return nil, err
		}
		settings, err := imageproc.ParseSettings(raw)
		if err != nil {
			return nil, err
		}
		settings, err = override.Apply(settings)
		if err != nil {
			return nil, err
		}
		frameID := randomID()
		res, err := tx.ExecContext(ctx, `INSERT INTO assignments(device_uuid,image_id,frame_id,settings_json,state,queued_at) VALUES(?,?,?,?,?,?)`, uuid, imageID, frameID, settings.JSON(), "queued", now)
		if err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		out = append(out, Assignment{ID: id, DeviceUUID: uuid, FrameID: frameID, State: "queued", QueuedAt: parseTime(now)})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func randomID() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	v := binary.LittleEndian.Uint32(b[:])
	if v == 0 {
		v = 1
	}
	return v
}

func (s *Store) Pending(ctx context.Context, uuid string) (Pending, error) {
	var p Pending
	var raw, queued string
	err := s.DB.QueryRowContext(ctx, `SELECT a.id,a.device_uuid,a.frame_id,a.state,a.queued_at,a.last_error,d.width,d.height,i.content_type,i.source,a.settings_json FROM assignments a JOIN images i ON i.id=a.image_id JOIN devices d ON d.uuid=a.device_uuid WHERE a.device_uuid=? AND a.state!='delivered' ORDER BY a.id DESC LIMIT 1`, uuid).Scan(&p.ID, &p.DeviceUUID, &p.FrameID, &p.State, &queued, &p.LastError, &p.Width, &p.Height, &p.ContentType, &p.Source, &raw)
	p.QueuedAt = parseTime(queued)
	if err == nil {
		p.Settings, err = imageproc.ParseSettings(raw)
	}
	return p, err
}

func (s *Store) PrepareSend(ctx context.Context, id int64, sequence uint32) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE assignments SET sent_sequence=?,acknowledged_at=NULL WHERE id=?`, sequence, id)
	return err
}

func (s *Store) MarkSent(ctx context.Context, id int64, sendErr error) error {
	state, msg := "sent", ""
	if sendErr != nil {
		state, msg = "error", sendErr.Error()
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE assignments SET state=?,last_error=? WHERE id=?`, state, msg, id)
	return err
}

func (s *Store) MarkAcknowledged(ctx context.Context, uuid string, sequence uint32) (int64, bool, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM assignments WHERE device_uuid=? AND sent_sequence=? AND state IN ('queued','sent') AND acknowledged_at IS NULL ORDER BY id DESC LIMIT 1`, uuid, sequence).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE assignments SET acknowledged_at=? WHERE id=?`, nowString(), id)
	return id, err == nil, err
}

func (s *Store) latestAssignment(ctx context.Context, uuid string) (*Assignment, error) {
	var a Assignment
	var queued, delivered, acknowledged sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id,device_uuid,frame_id,state,queued_at,delivered_at,acknowledged_at,last_error FROM assignments WHERE device_uuid=? ORDER BY id DESC LIMIT 1`, uuid).Scan(&a.ID, &a.DeviceUUID, &a.FrameID, &a.State, &queued, &delivered, &acknowledged, &a.LastError)
	if err != nil {
		return nil, err
	}
	a.QueuedAt = parseTime(queued.String)
	if delivered.Valid {
		t := parseTime(delivered.String)
		a.DeliveredAt = &t
	}
	if acknowledged.Valid {
		t := parseTime(acknowledged.String)
		a.AcknowledgedAt = &t
	}
	return &a, nil
}

func (s *Store) AddEvent(ctx context.Context, uuid, typ string, data any) (Event, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Event{}, err
	}
	now := nowString()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO events(device_uuid,type,data_json,created_at) VALUES(?,?,?,?)`, uuid, typ, string(raw), now)
	if err != nil {
		return Event{}, err
	}
	id, _ := res.LastInsertId()
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM events WHERE created_at < datetime('now','-7 days') OR id <= (SELECT COALESCE(MAX(id),0)-10000 FROM events)`)
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM status_samples WHERE created_at < datetime('now','-7 days')`)
	return Event{ID: id, DeviceUUID: uuid, Type: typ, Data: raw, CreatedAt: parseTime(now)}, nil
}

func (s *Store) Events(ctx context.Context, after int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,device_uuid,type,data_json,created_at FROM events WHERE id>? ORDER BY id LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var raw, created string
		if err := rows.Scan(&e.ID, &e.DeviceUUID, &e.Type, &raw, &created); err != nil {
			return nil, err
		}
		e.Data = json.RawMessage(raw)
		e.CreatedAt = parseTime(created)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) AllUUIDs(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT uuid FROM devices ORDER BY uuid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) DesiredPreview(ctx context.Context, uuid string) ([]byte, string, error) {
	var src, settings string
	var data []byte
	var width, height int
	err := s.DB.QueryRowContext(ctx, `SELECT i.content_type,i.source,a.settings_json,d.width,d.height FROM assignments a JOIN images i ON i.id=a.image_id JOIN devices d ON d.uuid=a.device_uuid WHERE a.device_uuid=? ORDER BY a.id DESC LIMIT 1`, uuid).Scan(&src, &data, &settings, &width, &height)
	if err != nil {
		return nil, "", err
	}
	img, err := imageproc.Decode(data, src)
	if err != nil {
		return nil, "", err
	}
	set, err := imageproc.ParseSettings(settings)
	if err != nil {
		return nil, "", err
	}
	_, preview, err := imageproc.Process(img, width, height, set)
	return preview, "image/png", err
}

func (s *Store) Debug() string { return fmt.Sprintf("sqlite store %p", s) }
