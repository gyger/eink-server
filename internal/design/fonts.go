package design

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/tdewolff/canvas"
	canvasfont "github.com/tdewolff/font"
)

//go:embed assets/*.ttf assets/OFL.txt
var embeddedFonts embed.FS

var fontConfiguration struct {
	sync.Mutex
	ready bool
}

// ConfigureFonts makes the embedded Noto families, an optional application
// font directory, and optionally the OS font directories visible to the SVG
// parser. It returns a cleanup function for the private extracted font cache.
func ConfigureFonts(fontDirectory string, useSystemFonts bool) (func(), error) {
	fontConfiguration.Lock()
	defer fontConfiguration.Unlock()
	cleanup, err := configureFonts(fontDirectory, useSystemFonts)
	if err == nil {
		fontConfiguration.ready = true
	}
	return cleanup, err
}

func ensureFonts() error {
	fontConfiguration.Lock()
	defer fontConfiguration.Unlock()
	if fontConfiguration.ready {
		return nil
	}
	_, err := configureFonts("", true)
	if err == nil {
		fontConfiguration.ready = true
	}
	return err
}

func configureFonts(fontDirectory string, useSystemFonts bool) (func(), error) {
	tempDir, err := os.MkdirTemp("", "eink-server-fonts-")
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tempDir) }
	entries, err := embeddedFonts.ReadDir("assets")
	if err != nil {
		cleanup()
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".ttf" {
			continue
		}
		data, err := embeddedFonts.ReadFile("assets/" + entry.Name())
		if err != nil {
			cleanup()
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(tempDir, entry.Name()), data, 0o600); err != nil {
			cleanup()
			return nil, err
		}
	}
	directories := []string{tempDir}
	if fontDirectory != "" {
		info, err := os.Stat(fontDirectory)
		if err == nil && !info.IsDir() {
			cleanup()
			return nil, fmt.Errorf("font_directory is not a directory: %s", fontDirectory)
		}
		if err == nil {
			directories = append(directories, fontDirectory)
		} else if !errors.Is(err, os.ErrNotExist) {
			cleanup()
			return nil, err
		}
	}
	if useSystemFonts {
		directories = append(directories, canvasfont.DefaultFontDirs()...)
	}
	if err := canvas.CacheSystemFonts(filepath.Join(tempDir, "font-cache.json"), directories); err != nil {
		cleanup()
		return nil, err
	}
	return cleanup, nil
}
