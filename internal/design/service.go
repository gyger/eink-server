package design

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"joantablet/server/internal/action"
	"joantablet/server/internal/events"
	"joantablet/server/internal/store"
)

var NamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type Notifier interface{ Notify(string) }
type Connections interface{ IsConnected(string) bool }

type Service struct {
	Store           *store.Store
	Hub             *events.Hub
	Log             *slog.Logger
	Actions         *action.Runner
	Notifier        Notifier
	Connections     Connections
	SystemName      string
	DesignDirectory string
	DefaultDesign   string
	Compiler        Compiler
	Now             func() time.Time
	renderMu        sync.Map
}

const builtinStatus = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 758" data-refresh="1m">
<rect width="1024" height="758" fill="white"/>
<text x="250" y="405" text-anchor="middle" font-family="Noto Sans" font-size="154" font-weight="400" data-value="${system.time}">18:30</text>
<line x1="500" y1="120" x2="500" y2="625" stroke="#b8b8b8" stroke-width="2"/>
<style>.calendar-title{font-weight:500}.calendar-weekday{font-weight:600}.calendar-outside{fill:#999}.calendar-today{fill:#000}.calendar-today-text{fill:#fff}</style>
<g data-widget="calendar" data-x="520" data-y="55" data-width="460" data-height="570" data-week-start="monday" data-spillover="true"/>
<g font-family="Noto Sans" fill="black">
  <text x="45" y="710" font-size="24" font-weight="600" data-value="${device.location}">Location</text>
  <text x="979" y="710" text-anchor="end" font-size="21" font-weight="500" data-value="${device.name} ${device.temperature} °C · Batt ${device.battery}%">Room 20 °C · Batt 98%</text>
</g>
</svg>`
const builtinTouch = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 758"><rect width="1024" height="758" fill="white"/><text x="50" y="70" font-family="sans-serif" font-size="42">Touch action test</text><rect id="button-one" x="80" y="140" width="380" height="220" rx="20" fill="#dddddd" stroke="black" stroke-width="5" data-action="button.one" data-region="button-one"/><text x="170" y="270" font-family="sans-serif" font-size="44">Button one</text><rect id="button-two" x="560" y="140" width="380" height="220" rx="20" fill="#dddddd" stroke="black" stroke-width="5" data-action="button.two" data-region="button-two"/><text x="650" y="270" font-family="sans-serif" font-size="44">Button two</text></svg>`

//go:embed builtins/eink-verification.svg
var builtinEInkVerification []byte

func (s *Service) Init(ctx context.Context) error { return s.Reload(ctx) }

func (s *Service) Reload(ctx context.Context) error {
	for _, d := range []store.Design{
		{ID: "builtin:status", Name: "Time and calendar", Source: "builtin", SVG: []byte(builtinStatus)},
		{ID: "builtin:touch-demo", Name: "Touch demo", Source: "builtin", SVG: []byte(builtinTouch)},
		{ID: "builtin:eink-verification", Name: "E Ink renderer and protocol verification", Source: "builtin", SVG: builtinEInkVerification},
	} {
		if err := s.validate(d.SVG); err != nil {
			return fmt.Errorf("built-in %s: %w", d.ID, err)
		}
		if err := s.Store.PutDesign(ctx, d); err != nil {
			return err
		}
	}
	if _, err := s.Store.DB.ExecContext(ctx, `DELETE FROM designs WHERE source='file'`); err != nil {
		return err
	}
	if s.DesignDirectory == "" {
		return nil
	}
	entries, err := os.ReadDir(s.DesignDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.ToLower(filepath.Ext(entry.Name())) != ".svg" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if !NamePattern.MatchString(name) {
			s.Log.Warn("filesystem design ignored", "file", entry.Name(), "error", "invalid name")
			continue
		}
		source, err := os.ReadFile(filepath.Join(s.DesignDirectory, entry.Name()))
		if err != nil {
			s.Log.Warn("filesystem design ignored", "file", entry.Name(), "error", err)
			continue
		}
		if err := s.validate(source); err != nil {
			s.Log.Warn("filesystem design ignored", "file", entry.Name(), "error", err)
			continue
		}
		if err := s.Store.PutDesign(ctx, store.Design{ID: "file:" + name, Name: name, Source: "file", SVG: source}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) validate(source []byte) error {
	_, err := s.Compiler.Render(source, 1024, 758, sampleValues(s.SystemName))
	return err
}

func sampleValues(system string) Values {
	return Values{"system.name": system, "system.time": "18:30", "system.date": "2026-02-12", "system.locale": "de-DE", "device.name": "Tablet", "device.location": "Location", "device.uuid": "00000000-0000-0000-0000-000000000000", "device.battery": "100", "device.temperature": "23", "device.humidity": "42", "device.width": "1024", "device.height": "758", "device.firmware": "0.0.0", "device.display_state": "0"}
}

func (s *Service) Values(d store.Device) Values {
	return s.valuesAt(d, s.now())
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) valuesAt(d store.Device, now time.Time) Values {
	name := d.Name
	if name == "" {
		name = d.UUID[:12]
	}
	zone, err := time.LoadLocation(d.Timezone)
	if err != nil {
		zone = time.UTC
	}
	local := now.In(zone)
	return Values{"system.name": s.SystemName, "system.time": local.Format("15:04"), "system.date": local.Format("2006-01-02"), "system.locale": d.Locale, "device.name": name, "device.location": d.Location, "device.uuid": d.UUID, "device.battery": strconv.FormatUint(uint64(d.Battery), 10), "device.temperature": strconv.FormatInt(int64(d.Temperature), 10), "device.humidity": strconv.FormatUint(uint64(d.Humidity), 10), "device.width": strconv.FormatUint(uint64(d.Width), 10), "device.height": strconv.FormatUint(uint64(d.Height), 10), "device.firmware": d.Firmware, "device.display_state": strconv.FormatUint(uint64(d.DisplayState), 10)}
}

func (s *Service) Designs(ctx context.Context) ([]store.Design, error) { return s.Store.Designs(ctx) }

func (s *Service) PutDatabaseDesign(ctx context.Context, name string, source []byte) (store.Design, error) {
	if !NamePattern.MatchString(name) {
		return store.Design{}, errors.New("invalid design name")
	}
	if err := s.validate(source); err != nil {
		return store.Design{}, err
	}
	d := store.Design{ID: "db:" + name, Name: name, Source: "db", SVG: source}
	return d, s.Store.PutDesign(ctx, d)
}

func (s *Service) DeleteDatabaseDesign(ctx context.Context, name string) error {
	return s.Store.DeleteDesign(ctx, "db:"+name)
}

func (s *Service) Assign(ctx context.Context, uuid, designID string) (store.Assignment, error) {
	d, err := s.Store.GetDesign(ctx, designID)
	if err != nil {
		return store.Assignment{}, err
	}
	if err := s.Store.SetActiveDesign(ctx, uuid, d.ID, d.SVG); err != nil {
		return store.Assignment{}, err
	}
	return s.Render(ctx, uuid, true)
}

func (s *Service) AssignInline(ctx context.Context, uuid string, source []byte) (store.Assignment, error) {
	if err := s.validate(source); err != nil {
		return store.Assignment{}, err
	}
	if err := s.Store.SetActiveDesign(ctx, uuid, "inline:"+uuid, source); err != nil {
		return store.Assignment{}, err
	}
	return s.Render(ctx, uuid, true)
}

func (s *Service) Clear(ctx context.Context, uuid string) error {
	return s.Store.ClearActiveDesign(ctx, uuid)
}

func (s *Service) Render(ctx context.Context, uuid string, force bool) (store.Assignment, error) {
	lockValue, _ := s.renderMu.LoadOrStore(uuid, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	active, err := s.Store.ActiveDesign(ctx, uuid)
	if err != nil {
		return store.Assignment{}, err
	}
	device, err := s.Store.GetDevice(ctx, uuid)
	if err != nil {
		return store.Assignment{}, err
	}
	values := s.Values(device)
	if !force && active.ValuesHash != "" && store.ValuesHash(values, active.Dependencies) == active.ValuesHash {
		return store.Assignment{}, nil
	}
	out, err := s.Compiler.RenderWithOptions(active.SVG, int(device.Width), int(device.Height), values, RenderOptions{Smooth: device.Settings.Rendering == "smooth"})
	if err != nil {
		return store.Assignment{}, err
	}
	hash := store.ValuesHash(values, out.Dependencies)
	actions := make([]store.InteractionRect, len(out.Actions))
	for i, region := range out.Actions {
		actions[i] = store.InteractionRect{X: region.X, Y: region.Y, Width: region.Width, Height: region.Height, Name: region.Name, Action: region.Action, Region: region.Region, Order: region.Order}
	}
	assignment, err := s.Store.QueueDesignFrame(ctx, uuid, active.DesignID, out.PageID, out.PNG, actions)
	if err != nil {
		return store.Assignment{}, err
	}
	if err := s.Store.UpdateActiveDesignRender(ctx, uuid, hash, out.PageID, out.Dependencies, int(out.Refresh/time.Second)); err != nil {
		return store.Assignment{}, err
	}
	s.emit(ctx, uuid, "design.rendered", map[string]any{"design_id": active.DesignID, "page_id": out.PageID, "assignment_id": assignment.ID, "frame_id": assignment.FrameID})
	s.emit(ctx, uuid, "image.queued", map[string]any{"assignment_id": assignment.ID, "frame_id": assignment.FrameID, "design_id": active.DesignID})
	if s.Notifier != nil {
		s.Notifier.Notify(uuid)
	}
	return assignment, nil
}

func (s *Service) RunScheduler(ctx context.Context) {
	for {
		now := s.now()
		next := now.Truncate(time.Minute).Add(time.Minute)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		uuids, err := s.Store.RefreshingDesignUUIDs(ctx)
		if err != nil {
			s.Log.Warn("loading refreshing designs", "error", err)
			continue
		}
		for _, uuid := range uuids {
			if s.Connections != nil && !s.Connections.IsConnected(uuid) {
				continue
			}
			active, err := s.Store.ActiveDesign(ctx, uuid)
			if err != nil || active.RefreshSeconds <= 0 || next.Unix()%int64(active.RefreshSeconds) != 0 {
				continue
			}
			if _, err := s.Render(ctx, uuid, false); err != nil && !errors.Is(err, sql.ErrNoRows) {
				s.Log.Warn("scheduled design render", "uuid", uuid, "error", err)
			}
		}
	}
}

func (s *Service) StatusChanged(ctx context.Context, uuid string) {
	if _, err := s.Render(ctx, uuid, false); err != nil && err != sql.ErrNoRows {
		s.Log.Warn("rendering dynamic design", "uuid", uuid, "error", err)
	}
}

func (s *Service) DeviceEnrolled(ctx context.Context, uuid string) {
	if s.DefaultDesign == "" {
		return
	}
	if _, err := s.Assign(ctx, uuid, s.DefaultDesign); err != nil {
		s.Log.Warn("assigning default design", "uuid", uuid, "design_id", s.DefaultDesign, "error", err)
	}
}

func (s *Service) Touch(ctx context.Context, uuid string, frameID, x, y, rawX, rawY uint32) {
	s.emit(ctx, uuid, "touch.tap", map[string]any{"frame_id": frameID, "x": x, "y": y, "raw_x": rawX, "raw_y": rawY})
	m, err := s.Store.InteractionForFrame(ctx, uuid, frameID)
	if err != nil {
		if err != sql.ErrNoRows {
			s.Log.Warn("loading touch interaction map", "uuid", uuid, "frame_id", frameID, "error", err)
		}
		return
	}
	sort.SliceStable(m.Actions, func(i, j int) bool { return m.Actions[i].Order > m.Actions[j].Order })
	for _, region := range m.Actions {
		if int(x) >= region.X && int(y) >= region.Y && int(x) < region.X+region.Width && int(y) < region.Y+region.Height {
			s.Log.Info("touch action", "uuid", uuid, "frame_id", frameID, "action", region.Action, "x", x, "y", y)
			s.Actions.Submit(ctx, action.Invocation{Action: region.Action, DeviceUUID: uuid, DesignID: m.DesignID, PageID: m.PageID, FrameID: frameID, ElementID: region.Name, Region: region.Region, X: x, Y: y, Timestamp: time.Now().UTC()})
			return
		}
	}
}

func (s *Service) Metadata(ctx context.Context, d store.Design) (Output, error) {
	return s.Compiler.Render(d.SVG, 1024, 758, sampleValues(s.SystemName))
}

func (s *Service) emit(ctx context.Context, uuid, typ string, data any) {
	e, err := s.Store.AddEvent(ctx, uuid, typ, data)
	if err == nil {
		s.Hub.Publish(e)
	}
}
