// Package render separates content generation from PV3 delivery. The first
// renderer handles uploaded images; later renderers can consume device/touch
// events and produce frames without changing the gateway.
package render

import (
	"context"
	"encoding/json"

	"eink-server/internal/imageproc"
)

type Device struct {
	UUID          string
	Width, Height int
}
type Event struct {
	Type string
	Data json.RawMessage
}
type Input struct {
	Device      Device
	Event       *Event
	ContentType string
	Source      []byte
	Settings    imageproc.Settings
}
type Frame struct {
	Packed4Bit []byte
	PreviewPNG []byte
}

type Renderer interface {
	Render(context.Context, Input) (Frame, error)
}

type UploadedImage struct{}

func (UploadedImage) Render(_ context.Context, in Input) (Frame, error) {
	img, err := imageproc.Decode(in.Source, in.ContentType)
	if err != nil {
		return Frame{}, err
	}
	pixels, preview, err := imageproc.Process(img, in.Device.Width, in.Device.Height, in.Settings)
	if err == nil {
		// The verified 6-inch tablet panel displays the raw PV3 framebuffer rotated by
		// 180 degrees relative to the intended screen orientation. Keep previews
		// and user rotation settings intuitive and transform only wire pixels.
		pixels = imageproc.RotatePacked180(pixels)
	}
	return Frame{Packed4Bit: pixels, PreviewPNG: preview}, err
}
