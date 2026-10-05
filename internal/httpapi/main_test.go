package httpapi

import (
	"os"
	"testing"

	"eink-server/internal/design"
)

// Tests only need the embedded Noto fonts; skipping the system font scan
// keeps the suite independent of the host's font installation.
func TestMain(m *testing.M) {
	cleanup, err := design.ConfigureFonts("", false)
	if err != nil {
		panic(err)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}
