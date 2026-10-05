package design

import (
	"os"
	"testing"
)

// Tests only need the embedded Noto fonts; skipping the system font scan
// keeps the suite fast and independent of the host's font installation.
func TestMain(m *testing.M) {
	cleanup, err := ConfigureFonts("", false)
	if err != nil {
		panic(err)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}
