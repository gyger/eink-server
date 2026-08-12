package pv3

import "errors"

type Touch struct {
	UUID      string
	UUIDBytes [16]byte
	FrameID   uint32
	RawX      uint32
	RawY      uint32
}

// ParseTouch decodes the one-record completed-contact message observed on the
// verified 6-inch tablet. It does not invent down/move/up phases that are absent from captures.
func ParseTouch(rec Record) (Touch, error) {
	if rec.Type != PacketApplication || rec.Word2 != 0 || len(rec.Payload) != 56 {
		return Touch{}, errors.New("invalid touch record shape")
	}
	p := rec.Payload
	if le.Uint32(p[0:4]) != 0 || le.Uint32(p[20:24]) != MessageTouch ||
		le.Uint32(p[24:28]) != 0xffffffff || le.Uint32(p[28:32]) != 20 ||
		le.Uint32(p[32:36]) != 0 || le.Uint32(p[40:44]) != 0 ||
		le.Uint32(p[44:48]) != 0 {
		return Touch{}, errors.New("invalid touch payload")
	}
	var touch Touch
	copy(touch.UUIDBytes[:], p[4:20])
	touch.UUID = formatUUID(touch.UUIDBytes)
	touch.FrameID = le.Uint32(p[36:40])
	touch.RawX = le.Uint32(p[48:52])
	touch.RawY = le.Uint32(p[52:56])
	return touch, nil
}

// PhysicalCoordinates converts the panel's native 180-degree-rotated touch
// coordinates into the same top-left-origin space used by uploaded images.
func (t Touch) PhysicalCoordinates(width, height uint32) (uint32, uint32, error) {
	if width == 0 || height == 0 || t.RawX >= width || t.RawY >= height {
		return 0, 0, errors.New("touch coordinate outside display bounds")
	}
	return width - 1 - t.RawX, height - 1 - t.RawY, nil
}
