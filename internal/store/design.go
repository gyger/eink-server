package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"eink-server/internal/imageproc"
)

type Design struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Source    string    `json:"source"`
	SVG       []byte    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ActiveDesign struct {
	DeviceUUID     string
	DesignID       string
	SVG            []byte
	Dependencies   []string
	ValuesHash     string
	PageID         string
	RefreshSeconds int
}

type InteractionMap struct {
	DesignID string            `json:"design_id"`
	PageID   string            `json:"page_id"`
	Actions  []InteractionRect `json:"actions"`
}

type InteractionRect struct {
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Name   string `json:"name,omitempty"`
	Action string `json:"action,omitempty"`
	Region string `json:"region,omitempty"`
	Order  int    `json:"order"`
}

type Action struct {
	Name      string            `json:"name"`
	Source    string            `json:"source"`
	Kind      string            `json:"type"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers,omitempty"`
	TimeoutMS int               `json:"timeout_ms"`
	UpdatedAt time.Time         `json:"updated_at"`
}

func (s *Store) PutDesign(ctx context.Context, d Design) error {
	now := nowString()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO designs(id,name,source,svg,created_at,updated_at) VALUES(?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name,source=excluded.source,svg=excluded.svg,updated_at=excluded.updated_at`, d.ID, d.Name, d.Source, d.SVG, now, now)
	return err
}

func (s *Store) Designs(ctx context.Context) ([]Design, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,source,svg,created_at,updated_at FROM designs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Design, 0)
	for rows.Next() {
		var d Design
		var created, updated string
		if err := rows.Scan(&d.ID, &d.Name, &d.Source, &d.SVG, &created, &updated); err != nil {
			return nil, err
		}
		d.CreatedAt, d.UpdatedAt = parseTime(created), parseTime(updated)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetDesign(ctx context.Context, id string) (Design, error) {
	var d Design
	var created, updated string
	err := s.DB.QueryRowContext(ctx, `SELECT id,name,source,svg,created_at,updated_at FROM designs WHERE id=?`, id).Scan(&d.ID, &d.Name, &d.Source, &d.SVG, &created, &updated)
	d.CreatedAt, d.UpdatedAt = parseTime(created), parseTime(updated)
	return d, err
}

func (s *Store) DeleteDesign(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM designs WHERE id=? AND source='db'`, id)
	if err == nil {
		if n, _ := res.RowsAffected(); n == 0 {
			return sql.ErrNoRows
		}
	}
	return err
}

func (s *Store) SetActiveDesign(ctx context.Context, uuid, designID string, svg []byte) error {
	now := nowString()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO device_designs(device_uuid,design_id,svg,updated_at) SELECT uuid,?,?,? FROM devices WHERE uuid=?
ON CONFLICT(device_uuid) DO UPDATE SET design_id=excluded.design_id,svg=excluded.svg,dependencies_json='[]',values_hash='',page_id='',updated_at=excluded.updated_at`, designID, svg, now, uuid)
	if err == nil {
		if n, _ := res.RowsAffected(); n == 0 {
			return sql.ErrNoRows
		}
	}
	return err
}

func (s *Store) ClearActiveDesign(ctx context.Context, uuid string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM device_designs WHERE device_uuid=?`, uuid)
	return err
}

func (s *Store) ActiveDesign(ctx context.Context, uuid string) (ActiveDesign, error) {
	var d ActiveDesign
	var deps string
	err := s.DB.QueryRowContext(ctx, `SELECT device_uuid,design_id,svg,dependencies_json,values_hash,page_id,refresh_seconds FROM device_designs WHERE device_uuid=?`, uuid).Scan(&d.DeviceUUID, &d.DesignID, &d.SVG, &deps, &d.ValuesHash, &d.PageID, &d.RefreshSeconds)
	if err == nil {
		err = json.Unmarshal([]byte(deps), &d.Dependencies)
	}
	return d, err
}

func (s *Store) UpdateActiveDesignRender(ctx context.Context, uuid, hash, pageID string, deps []string, refreshSeconds int) error {
	raw, _ := json.Marshal(deps)
	_, err := s.DB.ExecContext(ctx, `UPDATE device_designs SET dependencies_json=?,values_hash=?,page_id=?,refresh_seconds=?,updated_at=? WHERE device_uuid=?`, raw, hash, pageID, refreshSeconds, nowString(), uuid)
	return err
}

func (s *Store) RefreshingDesignUUIDs(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT device_uuid FROM device_designs WHERE refresh_seconds > 0 ORDER BY device_uuid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return nil, err
		}
		out = append(out, uuid)
	}
	return out, rows.Err()
}

func ValuesHash(values map[string]string, deps []string) string {
	h := sha256.New()
	for _, key := range deps {
		h.Write([]byte(key))
		h.Write([]byte{0})
		h.Write([]byte(values[key]))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Store) QueueDesignFrame(ctx context.Context, uuid, designID, pageID string, pngSource []byte, actions []InteractionRect) (Assignment, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Assignment{}, err
	}
	defer tx.Rollback()
	now := nowString()
	res, err := tx.ExecContext(ctx, `INSERT INTO images(content_type,source,created_at) VALUES('image/png',?,?)`, pngSource, now)
	if err != nil {
		return Assignment{}, err
	}
	imageID, _ := res.LastInsertId()
	var settingsRaw string
	if err := tx.QueryRowContext(ctx, `SELECT settings_json FROM devices WHERE uuid=?`, uuid).Scan(&settingsRaw); err != nil {
		return Assignment{}, err
	}
	settings, err := imageproc.ParseSettings(settingsRaw)
	if err != nil {
		return Assignment{}, err
	}
	settings.Fit, settings.Rotation = "exact", 0
	frameID := randomID()
	res, err = tx.ExecContext(ctx, `INSERT INTO assignments(device_uuid,image_id,frame_id,settings_json,state,queued_at) VALUES(?,?,?,?,?,?)`, uuid, imageID, frameID, settings.JSON(), "queued", now)
	if err != nil {
		return Assignment{}, err
	}
	id, _ := res.LastInsertId()
	interaction, _ := json.Marshal(InteractionMap{DesignID: designID, PageID: pageID, Actions: actions})
	if _, err := tx.ExecContext(ctx, `INSERT INTO assignment_interactions(assignment_id,design_id,page_id,map_json) VALUES(?,?,?,?)`, id, designID, pageID, interaction); err != nil {
		return Assignment{}, err
	}
	if err := tx.Commit(); err != nil {
		return Assignment{}, err
	}
	return Assignment{ID: id, DeviceUUID: uuid, FrameID: frameID, State: "queued", QueuedAt: parseTime(now)}, nil
}

func (s *Store) InteractionForFrame(ctx context.Context, uuid string, frameID uint32) (InteractionMap, error) {
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT i.map_json FROM assignment_interactions i JOIN assignments a ON a.id=i.assignment_id WHERE a.device_uuid=? AND a.frame_id=? ORDER BY a.id DESC LIMIT 1`, uuid, frameID).Scan(&raw)
	var out InteractionMap
	if err == nil {
		err = json.Unmarshal([]byte(raw), &out)
	}
	return out, err
}

func (s *Store) PutAction(ctx context.Context, a Action) error {
	headers, err := json.Marshal(a.Headers)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO actions(name,source,kind,url,headers_json,timeout_ms,updated_at) VALUES(?,?,?,?,?,?,?)
ON CONFLICT(name) DO UPDATE SET source=excluded.source,kind=excluded.kind,url=excluded.url,headers_json=excluded.headers_json,timeout_ms=excluded.timeout_ms,updated_at=excluded.updated_at`, a.Name, a.Source, a.Kind, a.URL, headers, a.TimeoutMS, nowString())
	return err
}

func (s *Store) GetAction(ctx context.Context, name string) (Action, error) {
	var a Action
	var headers, updated string
	err := s.DB.QueryRowContext(ctx, `SELECT name,source,kind,url,headers_json,timeout_ms,updated_at FROM actions WHERE name=?`, name).Scan(&a.Name, &a.Source, &a.Kind, &a.URL, &headers, &a.TimeoutMS, &updated)
	if err == nil {
		err = json.Unmarshal([]byte(headers), &a.Headers)
		a.UpdatedAt = parseTime(updated)
	}
	return a, err
}

func (s *Store) Actions(ctx context.Context) ([]Action, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT name,source,kind,url,headers_json,timeout_ms,updated_at FROM actions ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Action, 0)
	for rows.Next() {
		var a Action
		var headers, updated string
		if err := rows.Scan(&a.Name, &a.Source, &a.Kind, &a.URL, &headers, &a.TimeoutMS, &updated); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(headers), &a.Headers); err != nil {
			return nil, err
		}
		a.UpdatedAt = parseTime(updated)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAction(ctx context.Context, name string, dynamicOnly bool) error {
	query := `DELETE FROM actions WHERE name=?`
	if dynamicOnly {
		query += ` AND source='db'`
	}
	res, err := s.DB.ExecContext(ctx, query, name)
	if err == nil {
		if n, _ := res.RowsAffected(); n == 0 {
			return sql.ErrNoRows
		}
	}
	return err
}

func (s *Store) ReconcileConfigActions(ctx context.Context, actions []Action) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM actions WHERE source='config'`); err != nil {
		return err
	}
	for _, a := range actions {
		headers, _ := json.Marshal(a.Headers)
		if _, err := tx.ExecContext(ctx, `INSERT INTO actions(name,source,kind,url,headers_json,timeout_ms,updated_at) VALUES(?,?,?,?,?,?,?)
ON CONFLICT(name) DO UPDATE SET source='config',kind=excluded.kind,url=excluded.url,headers_json=excluded.headers_json,timeout_ms=excluded.timeout_ms,updated_at=excluded.updated_at`, a.Name, "config", a.Kind, a.URL, headers, a.TimeoutMS, nowString()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
