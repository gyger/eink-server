// Package widget hosts sandboxed eink-widget-v1 WebAssembly modules.
package widget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"eink-server/internal/config"
	"eink-server/internal/design"
	extism "github.com/extism/go-sdk"
)

const (
	defaultTimeout   = 5 * time.Second
	defaultPages     = 1024 // 64 MiB, WebAssembly pages are 64 KiB.
	defaultHTTPBytes = 1 << 20
)

type module struct {
	compiled *extism.CompiledPlugin
}

type Registry struct{ modules map[string]module }

// New compiles all modules once. Each Render call creates a fresh isolated instance.
// baseDir is the directory containing the TOML file and is used for relative paths.
func New(ctx context.Context, baseDir string, configured map[string]config.WidgetConfig, embedded map[string][]byte) (*Registry, error) {
	r := &Registry{modules: map[string]module{}}
	for name, wasm := range embedded {
		definition := config.WidgetConfig{AllowedHosts: []string{"api.transitous.org"}}
		if err := r.add(ctx, name, wasm, definition); err != nil {
			r.Close(ctx)
			return nil, fmt.Errorf("widget %q: %w", name, err)
		}
	}
	for name, definition := range configured {
		path := definition.Module
		if !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, path)
		}
		wasm, err := os.ReadFile(path)
		if err != nil {
			r.Close(ctx)
			return nil, fmt.Errorf("widget %q: %w", name, err)
		}
		if err := r.add(ctx, name, wasm, definition); err != nil {
			r.Close(ctx)
			return nil, fmt.Errorf("widget %q: %w", name, err)
		}
	}
	return r, nil
}

func (r *Registry) add(ctx context.Context, name string, wasm []byte, c config.WidgetConfig) error {
	if len(wasm) == 0 {
		return errors.New("empty WebAssembly module")
	}
	timeout := defaultTimeout
	if c.Timeout != "" {
		timeout, _ = time.ParseDuration(c.Timeout)
	}
	pages := c.MaxMemoryPages
	if pages == 0 {
		pages = defaultPages
	}
	httpBytes := c.MaxHTTPResponseBytes
	if httpBytes == 0 {
		httpBytes = defaultHTTPBytes
	}
	manifest := extism.Manifest{Wasm: []extism.Wasm{extism.WasmData{Data: wasm}}, AllowedHosts: c.AllowedHosts, Config: c.Config, Timeout: uint64(timeout / time.Millisecond), Memory: &extism.ManifestMemory{MaxPages: pages, MaxHttpResponseBytes: httpBytes}}
	compiled, err := extism.NewCompiledPlugin(ctx, manifest, extism.PluginConfig{EnableWasi: true}, nil)
	if err != nil {
		return err
	}
	r.modules[name] = module{compiled: compiled}
	return nil
}

func (r *Registry) Registered(name string) bool { _, ok := r.modules[name]; return ok }

func (r *Registry) Render(ctx context.Context, name string, input design.WidgetInput) ([]byte, error) {
	m, ok := r.modules[name]
	if !ok {
		return nil, fmt.Errorf("unknown widget %q", name)
	}
	p, err := m.compiled.Instance(ctx, extism.PluginInstanceConfig{})
	if err != nil {
		return nil, err
	}
	defer p.Close(ctx)
	data, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	code, out, err := p.CallWithContext(ctx, "render", data)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("render returned status %d", code)
	}
	return out, nil
}

func (r *Registry) Close(ctx context.Context) error {
	var err error
	for _, m := range r.modules {
		err = errors.Join(err, m.compiled.Close(ctx))
	}
	return err
}
