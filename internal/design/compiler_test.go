package design

import "testing"

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
