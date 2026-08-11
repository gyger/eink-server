package design

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"joantablet/server/internal/action"
	"joantablet/server/internal/events"
	"joantablet/server/internal/store"
)

var NamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type Notifier interface{ Notify(string) }

type Service struct {
	Store           *store.Store
	Hub             *events.Hub
	Log             *slog.Logger
	Actions         *action.Runner
	Notifier        Notifier
	SystemName      string
	DesignDirectory string
	DefaultDesign   string
	Compiler        Compiler
}

const builtinStatus = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 758">
<rect width="1024" height="758" fill="white"/>
<text x="250" y="405" text-anchor="middle" font-family="Noto Sans" font-size="154" font-weight="400" data-value="${system.time}">18:30</text>
<line x1="500" y1="120" x2="500" y2="625" stroke="#b8b8b8" stroke-width="2"/>
<g font-family="Noto Sans" fill="black">
  <text x="752" y="105" text-anchor="middle" font-size="45" font-weight="500">February 2026</text>
  <g text-anchor="middle" font-size="21" font-weight="600"><text x="556" y="165">M</text><text x="621" y="165">T</text><text x="686" y="165">W</text><text x="751" y="165">T</text><text x="816" y="165">F</text><text x="881" y="165">S</text><text x="946" y="165">S</text></g>
  <g text-anchor="middle" font-size="27">
    <text x="946" y="220">1</text>
    <text x="556" y="280">2</text><text x="621" y="280">3</text><text x="686" y="280">4</text><text x="751" y="280">5</text><text x="816" y="280">6</text><text x="881" y="280">7</text><text x="946" y="280">8</text>
    <text x="556" y="340">9</text><text x="621" y="340">10</text><text x="686" y="340">11</text><circle cx="751" cy="331" r="25" fill="black"/><text x="751" y="340" fill="white">12</text><text x="816" y="340">13</text><text x="881" y="340">14</text><text x="946" y="340">15</text>
    <text x="556" y="400">16</text><text x="621" y="400">17</text><text x="686" y="400">18</text><text x="751" y="400">19</text><text x="816" y="400">20</text><text x="881" y="400">21</text><text x="946" y="400">22</text>
    <text x="556" y="460">23</text><text x="621" y="460">24</text><text x="686" y="460">25</text><text x="751" y="460">26</text><text x="816" y="460">27</text><text x="881" y="460">28</text>
  </g>
  <text x="45" y="710" font-size="24" font-weight="600" data-value="${device.location}">Location</text>
  <text x="260" y="710" font-size="21" font-weight="500" fill="#777777" data-value="· Updated ${system.time}">· Updated 18:30</text>
  <text x="979" y="710" text-anchor="end" font-size="21" font-weight="500" data-value="${device.name} ${device.temperature} °C · Batt ${device.battery}%">Room 20 °C · Batt 98%</text>
</g>
</svg>`
const builtinTouch = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 758"><rect width="1024" height="758" fill="white"/><text x="50" y="70" font-family="sans-serif" font-size="42">Touch action test</text><rect id="button-one" x="80" y="140" width="380" height="220" rx="20" fill="#dddddd" stroke="black" stroke-width="5" data-action="button.one" data-region="button-one"/><text x="170" y="270" font-family="sans-serif" font-size="44">Button one</text><rect id="button-two" x="560" y="140" width="380" height="220" rx="20" fill="#dddddd" stroke="black" stroke-width="5" data-action="button.two" data-region="button-two"/><text x="650" y="270" font-family="sans-serif" font-size="44">Button two</text></svg>`

func (s *Service) Init(ctx context.Context) error { return s.Reload(ctx) }

func (s *Service) Reload(ctx context.Context) error {
	for _, d := range []store.Design{{ID: "builtin:status", Name: "Time and calendar", Source: "builtin", SVG: []byte(builtinStatus)}, {ID: "builtin:touch-demo", Name: "Touch demo", Source: "builtin", SVG: []byte(builtinTouch)}} {
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
	return Values{"system.name": system, "system.time": "18:30", "device.name": "Tablet", "device.location": "Location", "device.uuid": "00000000-0000-0000-0000-000000000000", "device.battery": "100", "device.temperature": "23", "device.humidity": "42", "device.width": "1024", "device.height": "758", "device.firmware": "0.0.0", "device.display_state": "0"}
}

func (s *Service) Values(d store.Device) Values {
	name := d.Name
	if name == "" {
		name = d.UUID[:12]
	}
	return Values{"system.name": s.SystemName, "system.time": time.Now().Format("15:04"), "device.name": name, "device.location": d.Location, "device.uuid": d.UUID, "device.battery": strconv.FormatUint(uint64(d.Battery), 10), "device.temperature": strconv.FormatInt(int64(d.Temperature), 10), "device.humidity": strconv.FormatUint(uint64(d.Humidity), 10), "device.width": strconv.FormatUint(uint64(d.Width), 10), "device.height": strconv.FormatUint(uint64(d.Height), 10), "device.firmware": d.Firmware, "device.display_state": strconv.FormatUint(uint64(d.DisplayState), 10)}
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
	out, err := s.Compiler.Render(active.SVG, int(device.Width), int(device.Height), values)
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
	if err := s.Store.UpdateActiveDesignRender(ctx, uuid, hash, out.PageID, out.Dependencies); err != nil {
		return store.Assignment{}, err
	}
	s.emit(ctx, uuid, "design.rendered", map[string]any{"design_id": active.DesignID, "page_id": out.PageID, "assignment_id": assignment.ID, "frame_id": assignment.FrameID})
	s.emit(ctx, uuid, "image.queued", map[string]any{"assignment_id": assignment.ID, "frame_id": assignment.FrameID, "design_id": active.DesignID})
	if s.Notifier != nil {
		s.Notifier.Notify(uuid)
	}
	return assignment, nil
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
