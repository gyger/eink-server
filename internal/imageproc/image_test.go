package imageproc

import (
	"image"
	"image/color"
	"testing"
)

func TestContainAndPack(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 2, 1))
	src.SetGray(0, 0, color.Gray{0})
	src.SetGray(1, 0, color.Gray{255})
	packed, preview, err := Process(src, 4, 4, Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if len(packed) != 8 || len(preview) == 0 {
		t.Fatalf("packed=%d preview=%d", len(packed), len(preview))
	}
	if packed[0] != 0xff || packed[len(packed)-1] != 0xff {
		t.Fatal("contain background is not white")
	}
}

func TestPackUsesPV3LowNibbleFirstOrder(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 2, 1))
	src.SetGray(0, 0, color.Gray{0})
	src.SetGray(1, 0, color.Gray{255})
	settings := Defaults()
	settings.Fit = "exact"
	settings.Rendering = "smooth"
	packed, _, err := Process(src, 2, 1, settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(packed) != 1 || packed[0] != 0xf0 {
		t.Fatalf("packed=%x, want f0 (right in high nibble, left in low)", packed)
	}
}

func TestExactRejectsMismatch(t *testing.T) {
	s := Defaults()
	s.Fit = "exact"
	_, _, err := Process(image.NewGray(image.Rect(0, 0, 2, 2)), 4, 4, s)
	if err == nil {
		t.Fatal("expected mismatch error")
	}
}
func TestSettingsValidation(t *testing.T) {
	s := Defaults()
	s.Rotation = 45
	if s.Validate() == nil {
		t.Fatal("expected rotation error")
	}
	s = Defaults()
	s.Dither = "floyd-steinberg"
	s.Invert = true
	if s.Validate() != nil {
		t.Fatal("valid settings rejected")
	}
	s.Rendering = "unknown"
	if s.Validate() == nil {
		t.Fatal("expected rendering error")
	}
}

func TestSuppressAntialiasPreservesFieldsAndHardensEdges(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 5, 1))
	copy(img.Pix, []byte{85, 85, 0, 100, 255})
	suppressAntialias(img)
	if img.Pix[0] != 85 || img.Pix[1] != 85 {
		t.Fatalf("uniform gray field changed: %v", img.Pix)
	}
	if img.Pix[3] != 0 {
		t.Fatalf("antialiased edge was not hardened: %v", img.Pix)
	}
}

func TestVSSBeautifyExpandsFourBitRange(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 3, 1))
	copy(img.Pix, []byte{0, 102, 255})
	vssBeautify(img, 1.1)
	if img.Pix[0] != 0 || img.Pix[1] != 91 || img.Pix[2] != 255 {
		t.Fatalf("unexpected VSS levels: %v", img.Pix)
	}
}

func TestRotatePacked180(t *testing.T) {
	// Pixels 1,2,3,4 become 4,3,2,1 after a 180-degree rotation.
	got := RotatePacked180([]byte{0x12, 0x34})
	want := []byte{0x43, 0x21}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("RotatePacked180() = %x, want %x", got, want)
	}
}
