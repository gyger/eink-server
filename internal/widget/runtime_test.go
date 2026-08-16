package widget

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"eink-server/internal/design"
)

func TestEmbeddedDeparturesPlaceholderThroughExtism(t *testing.T) {
	r, err := New(context.Background(), t.TempDir(), nil, map[string][]byte{"departures": DeparturesWASM})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	in := design.WidgetInput{Version: "eink-widget-v1", Instance: "next-buses", Now: time.Now().Format(time.RFC3339), Timezone: "Europe/Berlin", Locale: "de-DE", Config: map[string]string{"stop-id": "", "title": "Departures", "rows": "5", "modes": "BUS"}}
	in.Viewport.Width, in.Viewport.Height = 610, 675
	out, err := r.Render(context.Background(), "departures", in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Configure stop") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestEmbeddedDeparturesLiveTransitous(t *testing.T) {
	if os.Getenv("TRANSITOUS_LIVE") == "" {
		t.Skip("set TRANSITOUS_LIVE=1")
	}
	r, err := New(context.Background(), t.TempDir(), nil, map[string][]byte{"departures": DeparturesWASM})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	in := design.WidgetInput{Version: "eink-widget-v1", Instance: "next-buses", Now: time.Now().Format(time.RFC3339), Timezone: "Europe/Berlin", Locale: "de-DE", Config: map[string]string{"stop-id": "de-DELFI_de:10041:10905::1090501", "title": "Departures", "rows": "5", "modes": "BUS"}}
	in.Viewport.Width, in.Viewport.Height = 610, 675
	out, err := r.Render(context.Background(), "departures", in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Data: Transitous") || strings.Contains(string(out), "Configure stop") {
		t.Fatalf("unexpected output: %s", out)
	}
	if !strings.Contains(string(out), ">Departures</text>") || !strings.Contains(string(out), ">Universität Mensa, Saarbrücken</text>") {
		t.Fatalf("station heading missing: %s", out)
	}
}
