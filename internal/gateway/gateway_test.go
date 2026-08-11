package gateway

import (
	"bytes"
	"testing"
)

func TestChangedPrimitive(t *testing.T) {
	previous := bytes.Repeat([]byte{0xff}, 4*4/2)
	current := append([]byte(nil), previous...)
	current[1*2+1] = 0x01
	current[2*2+1] = 0x23

	got := changedPrimitive(current, previous, 4, 4, 4, 4)
	if got.X != 2 || got.Y != 1 || got.Width != 2 || got.Height != 2 || !bytes.Equal(got.Pixels, []byte{0x01, 0x23}) {
		t.Fatalf("primitive=%+v", got)
	}
}

func TestChangedPrimitiveUsesFullFrameWithoutPrevious(t *testing.T) {
	current := []byte{0x01, 0x23, 0x45, 0x67}
	got := changedPrimitive(current, nil, 4, 2, 0, 0)
	if got.X != 0 || got.Y != 0 || got.Width != 4 || got.Height != 2 || !bytes.Equal(got.Pixels, current) {
		t.Fatalf("primitive=%+v", got)
	}
}
