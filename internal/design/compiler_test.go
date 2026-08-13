package design

import (
	"strings"
	"testing"
	"time"
)

func TestRenderDynamicSVGAndMetadata(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 50"><rect width="100" height="50" fill="white"/><text x="5" y="15" data-value="${device.temperature} °C">--</text><rect x="10" y="20" width="30" height="20" fill="none" data-action="lights" data-region="button"/></svg>`)
	out, err := (Compiler{}).Render(svg, 200, 100, Values{"device.temperature": "23"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.PNG) == 0 || len(out.Actions) != 1 || out.Actions[0].X != 20 || out.Actions[0].Width != 60 || len(out.Regions) != 1 {
		t.Fatalf("output=%+v", out)
	}
	if len(out.Dependencies) != 1 || out.Dependencies[0] != "device.temperature" {
		t.Fatalf("dependencies=%v", out.Dependencies)
	}
}

func TestCalendarWidgetAndRefresh(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 700 600" data-refresh="1m"><g data-widget="calendar" data-x="0" data-y="0" data-width="700" data-height="600" data-week-start="monday" data-spillover="true"/></svg>`)
	out, err := (Compiler{}).Render(svg, 700, 600, Values{"system.date": "2024-02-29", "system.locale": "de-DE"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Refresh.String() != "1m0s" || len(out.Dependencies) != 2 || out.Dependencies[0] != "system.date" || out.Dependencies[1] != "system.locale" {
		t.Fatalf("output=%+v", out)
	}
	clean, _, err := compileXML(svg, 700, 600, Values{"system.date": "2024-02-29", "system.locale": "de-DE"})
	if err != nil || !strings.Contains(string(clean), "Februar 2024") || strings.Count(string(clean), `calendar-day`) != 42 || !strings.Contains(string(clean), `class="calendar-day calendar-today-text" text-anchor="middle" font-size="25.80" fill="white"`) || !strings.Contains(string(clean), `class="calendar-day calendar-outside" text-anchor="middle" font-size="25.80" fill="#999999"`) || !strings.Contains(string(clean), `cy="462.23"`) {
		t.Fatalf("calendar output invalid: err=%v xml=%s", err, clean)
	}
}

func TestNavigableCalendarEmitsWidgetTargets(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 700 600"><g id="main" data-widget="calendar" data-navigation="true" data-x="0" data-y="0" data-width="700" data-height="600"/></svg>`)
	out, err := (Compiler{}).Render(svg, 700, 600, Values{"system.date": "2026-08-13", "system.locale": "en-GB", "widget.main.month_offset": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Refresh != time.Minute || len(out.Actions) != 3 {
		t.Fatalf("refresh=%v actions=%+v", out.Refresh, out.Actions)
	}
	if out.Actions[0].Recipient != "widget" || out.Actions[0].Provider != "calendar" || out.Actions[0].Instance != "main" || out.Actions[0].Event != "previous" {
		t.Fatalf("target=%+v", out.Actions[0])
	}
	clean, _, err := compileXML(svg, 700, 600, Values{"system.date": "2026-08-13", "system.locale": "en-GB", "widget.main.month_offset": "1"})
	if err != nil || !strings.Contains(string(clean), "September 2026") || !strings.Contains(string(clean), "calendar-previous") {
		t.Fatalf("calendar output invalid: err=%v xml=%s", err, clean)
	}
}

func TestCalendarRejectsInvalidContract(t *testing.T) {
	values := Values{"system.date": "2026-08-12", "system.locale": "de-DE"}
	for _, svg := range []string{
		`<svg viewBox="0 0 10 10" data-refresh="30s"><rect/></svg>`,
		`<svg viewBox="0 0 10 10"><g data-widget="unknown"/></svg>`,
		`<svg viewBox="0 0 10 10"><g data-widget="calendar" data-x="0" data-y="0" data-width="10" data-height="10"><rect/></g></svg>`,
	} {
		if _, err := (Compiler{}).Render([]byte(svg), 10, 10, values); err == nil {
			t.Fatalf("invalid SVG accepted: %s", svg)
		}
	}
}

func TestRenderFirstPageAndRejectUnsafe(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><g data-page="one"><rect width="10" height="10"/></g><g data-page="two"><rect data-action="hidden" width="10" height="10"/></g></svg>`)
	out, err := (Compiler{}).Render(svg, 10, 10, Values{})
	if err != nil || out.PageID != "one" || len(out.Actions) != 0 {
		t.Fatalf("output=%+v err=%v", out, err)
	}
	for _, bad := range []string{
		`<svg viewBox="0 0 1 1"><script/></svg>`,
		`<svg viewBox="0 0 1 1"><image href="https://example.test/x.png"/></svg>`,
		`<svg viewBox="0 0 1 1"><text data-value="${unknown}">x</text></svg>`,
	} {
		if _, err := (Compiler{}).Render([]byte(bad), 10, 10, Values{}); err == nil {
			t.Fatalf("unsafe SVG accepted: %s", bad)
		}
	}
}

func TestEmbeddedBinaryImage(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><image width="1" height="1" href="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9ZrroAAAAASUVORK5CYII="/></svg>`)
	if out, err := (Compiler{}).Render(svg, 8, 8, Values{}); err != nil || len(out.PNG) == 0 {
		t.Fatalf("embedded binary image output=%d err=%v", len(out.PNG), err)
	}
}

func TestNativeResolutionRendering(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10" fill="white"/><line x1="1" y1="1" x2="9" y2="9" stroke="black" stroke-width="1"/></svg>`)
	out, err := (Compiler{}).RenderWithOptions(svg, 10, 10, Values{}, RenderOptions{Smooth: false})
	if err != nil || len(out.PNG) == 0 {
		t.Fatalf("native-resolution output=%d err=%v", len(out.PNG), err)
	}
}
