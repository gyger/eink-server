package imageproc

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"strings"
)

const (
	MaxUploadBytes = 20 << 20
	MaxPixels      = 20_000_000
)

type Settings struct {
	Fit        string `json:"fit"`
	Background string `json:"background"`
	Rotation   int    `json:"rotation"`
	Invert     bool   `json:"invert"`
	Dither     string `json:"dither"`
	Rendering  string `json:"rendering"`
}

type Override struct {
	Fit, Background, Dither, Rendering *string
	Rotation                           *int
	Invert                             *bool
}

func (o Override) Empty() bool {
	return o.Fit == nil && o.Background == nil && o.Dither == nil && o.Rendering == nil && o.Rotation == nil && o.Invert == nil
}
func (o Override) Apply(s Settings) (Settings, error) {
	if o.Fit != nil {
		s.Fit = *o.Fit
	}
	if o.Background != nil {
		s.Background = *o.Background
	}
	if o.Dither != nil {
		s.Dither = *o.Dither
	}
	if o.Rendering != nil {
		s.Rendering = *o.Rendering
	}
	if o.Rotation != nil {
		s.Rotation = *o.Rotation
	}
	if o.Invert != nil {
		s.Invert = *o.Invert
	}
	return s, s.Validate()
}

func Defaults() Settings {
	return Settings{Fit: "contain", Background: "white", Dither: "none", Rendering: "eink"}
}

func (s Settings) Validate() error {
	if s.Fit != "contain" && s.Fit != "cover" && s.Fit != "stretch" && s.Fit != "exact" {
		return errors.New("fit must be contain, cover, stretch, or exact")
	}
	if s.Background != "white" && s.Background != "black" {
		return errors.New("background must be white or black")
	}
	if s.Rotation != 0 && s.Rotation != 90 && s.Rotation != 180 && s.Rotation != 270 {
		return errors.New("rotation must be 0, 90, 180, or 270")
	}
	if s.Dither != "none" && s.Dither != "floyd-steinberg" {
		return errors.New("dither must be none or floyd-steinberg")
	}
	if s.Rendering != "eink" && s.Rendering != "smooth" {
		return errors.New("rendering must be eink or smooth")
	}
	return nil
}

func (s Settings) JSON() string { b, _ := json.Marshal(s); return string(b) }

func ParseSettings(raw string) (Settings, error) {
	s := Defaults()
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			return s, err
		}
	}
	return s, s.Validate()
}

func Decode(data []byte, contentType string) (image.Image, error) {
	if len(data) > MaxUploadBytes {
		return nil, errors.New("image exceeds 20 MiB")
	}
	var cfg image.Config
	var err error
	switch strings.Split(contentType, ";")[0] {
	case "image/png":
		cfg, err = png.DecodeConfig(bytes.NewReader(data))
	case "image/jpeg":
		cfg, err = jpeg.DecodeConfig(bytes.NewReader(data))
	default:
		return nil, errors.New("only image/png and image/jpeg are supported")
	}
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > MaxPixels {
		return nil, errors.New("invalid or oversized image dimensions")
	}
	var img image.Image
	if strings.HasPrefix(contentType, "image/png") {
		img, err = png.Decode(bytes.NewReader(data))
	} else {
		img, err = jpeg.Decode(bytes.NewReader(data))
	}
	return img, err
}

func Process(src image.Image, width, height int, settings Settings) ([]byte, []byte, error) {
	if err := settings.Validate(); err != nil {
		return nil, nil, err
	}
	if width <= 0 || height <= 0 || width*height > MaxPixels {
		return nil, nil, errors.New("invalid target dimensions")
	}
	if width%2 != 0 {
		return nil, nil, errors.New("4-bit PV3 encoding requires an even display width")
	}
	src = rotate(src, settings.Rotation)
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	if settings.Fit == "exact" && (sw != width || sh != height) {
		return nil, nil, errors.New("image dimensions do not exactly match display")
	}
	bg := uint8(255)
	if settings.Background == "black" {
		bg = 0
	}
	gray := image.NewGray(image.Rect(0, 0, width, height))
	for i := range gray.Pix {
		gray.Pix[i] = bg
	}
	dw, dh, ox, oy := width, height, 0, 0
	if settings.Fit == "contain" {
		scale := math.Min(float64(width)/float64(sw), float64(height)/float64(sh))
		dw, dh = max(1, int(math.Round(float64(sw)*scale))), max(1, int(math.Round(float64(sh)*scale)))
		ox, oy = (width-dw)/2, (height-dh)/2
	} else if settings.Fit == "cover" {
		scale := math.Max(float64(width)/float64(sw), float64(height)/float64(sh))
		dw, dh = max(1, int(math.Round(float64(sw)*scale))), max(1, int(math.Round(float64(sh)*scale)))
		ox, oy = (width-dw)/2, (height-dh)/2
	}
	resample(gray, src, ox, oy, dw, dh)
	if settings.Invert {
		for i := range gray.Pix {
			gray.Pix[i] = 255 - gray.Pix[i]
		}
	}
	if settings.Rendering == "eink" {
		suppressAntialias(gray)
		vssBeautify(gray, 1.1)
	}
	if settings.Dither == "floyd-steinberg" {
		dither(gray)
	} else {
		quantize(gray)
	}
	packed := make([]byte, width*height/2)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x += 2 {
			a := gray.GrayAt(x, y).Y / 17
			b := uint8(15)
			if x+1 < width {
				b = gray.GrayAt(x+1, y).Y / 17
			}
			// PV3 encoding 4 stores the left pixel in the low nibble and the
			// right pixel in the high nibble. This order was verified against
			// an official VSS 7.6.5 structured impulse capture.
			packed[(y*width+x)/2] = b<<4 | a
		}
	}
	var preview bytes.Buffer
	if err := png.Encode(&preview, gray); err != nil {
		return nil, nil, err
	}
	return packed, preview.Bytes(), nil
}

// RotatePacked180 converts an intended-orientation 4-bit grayscale frame to the
// native framebuffer orientation used by the verified 6-inch tablet. Two pixels are
// stored per byte, so rotating reverses the byte order and swaps both nibbles.
func RotatePacked180(packed []byte) []byte {
	out := make([]byte, len(packed))
	for i, value := range packed {
		out[len(packed)-1-i] = value<<4 | value>>4
	}
	return out
}

func rotate(src image.Image, degrees int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if degrees == 0 {
		return src
	}
	if degrees == 180 {
		d := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				d.Set(w-1-x, h-1-y, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return d
	}
	d := image.NewNRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if degrees == 90 {
				d.Set(h-1-y, x, src.At(b.Min.X+x, b.Min.Y+y))
			} else {
				d.Set(y, w-1-x, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
	}
	return d
}

func resample(dst *image.Gray, src image.Image, ox, oy, dw, dh int) {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	for dy := 0; dy < dh; dy++ {
		y := oy + dy
		if y < 0 || y >= dst.Bounds().Dy() {
			continue
		}
		sy := (float64(dy)+.5)*float64(sh)/float64(dh) - .5
		for dx := 0; dx < dw; dx++ {
			x := ox + dx
			if x < 0 || x >= dst.Bounds().Dx() {
				continue
			}
			sx := (float64(dx)+.5)*float64(sw)/float64(dw) - .5
			dst.SetGray(x, y, color.Gray{Y: sampleGray(src, b, sx, sy)})
		}
	}
}

func sampleGray(src image.Image, b image.Rectangle, x, y float64) uint8 {
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	fx, fy := x-float64(x0), y-float64(y0)
	get := func(px, py int) float64 {
		if px < 0 {
			px = 0
		}
		if py < 0 {
			py = 0
		}
		if px >= b.Dx() {
			px = b.Dx() - 1
		}
		if py >= b.Dy() {
			py = b.Dy() - 1
		}
		r, g, bl, _ := src.At(b.Min.X+px, b.Min.Y+py).RGBA()
		return (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)) / 257
	}
	a := get(x0, y0)*(1-fx) + get(x0+1, y0)*fx
	c := get(x0, y0+1)*(1-fx) + get(x0+1, y0+1)*fx
	return uint8(math.Round(a*(1-fy) + c*fy))
}

func quantize(img *image.Gray) {
	for i, v := range img.Pix {
		img.Pix[i] = uint8(math.Round(float64(v)/17)) * 17
	}
}

// suppressAntialias hardens only pixels on high-contrast edges. Uniform gray
// fields and low-contrast photographic detail are left alone.
func suppressAntialias(img *image.Gray) {
	source := append([]byte(nil), img.Pix...)
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			lo, hi := uint8(255), uint8(0)
			for dy := -1; dy <= 1; dy++ {
				yy := min(max(y+dy, 0), h-1)
				for dx := -1; dx <= 1; dx++ {
					xx := min(max(x+dx, 0), w-1)
					v := source[yy*img.Stride+xx]
					lo, hi = min(lo, v), max(hi, v)
				}
			}
			if int(hi)-int(lo) < 128 {
				continue
			}
			v := source[y*img.Stride+x]
			if int(v)-int(lo) <= int(hi)-int(v) {
				img.Pix[y*img.Stride+x] = lo
			} else {
				img.Pix[y*img.Stride+x] = hi
			}
		}
	}
}

// vssBeautify reproduces the grayscale path recovered from VSS beautify.cpp:
// a clipped linear LUT, integer 0..15 reduction, then range expansion.
func vssBeautify(img *image.Gray, gamma float64) {
	levels := make([]uint8, len(img.Pix))
	lo, hi := uint8(15), uint8(0)
	for i, value := range img.Pix {
		corrected := math.Trunc(float64(value) / gamma)
		level := uint8(math.Round(corrected * 15 / 255))
		levels[i] = level
		lo, hi = min(lo, level), max(hi, level)
	}
	if lo == hi {
		lo, hi = 0, 15
	}
	for i, level := range levels {
		img.Pix[i] = uint8(math.Round(float64(level-lo) * 255 / float64(hi-lo)))
	}
}

func dither(img *image.Gray) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	errs := make([]float64, w+2)
	next := make([]float64, w+2)
	for y := 0; y < h; y++ {
		clear(next)
		for x := 0; x < w; x++ {
			old := float64(img.GrayAt(x, y).Y) + errs[x+1]
			if old < 0 {
				old = 0
			}
			if old > 255 {
				old = 255
			}
			q := math.Round(old/17) * 17
			img.SetGray(x, y, color.Gray{uint8(q)})
			e := old - q
			errs[x+2] += e * 7 / 16
			next[x] += e * 3 / 16
			next[x+1] += e * 5 / 16
			next[x+2] += e / 16
		}
		errs, next = next, errs
	}
}

func ReadAllLimited(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, MaxUploadBytes+1))
}
