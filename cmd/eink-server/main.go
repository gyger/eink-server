package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"eink-server/internal/action"
	"eink-server/internal/config"
	"eink-server/internal/design"
	"eink-server/internal/events"
	"eink-server/internal/gateway"
	"eink-server/internal/httpapi"
	"eink-server/internal/imageproc"
	"eink-server/internal/store"
	"eink-server/internal/widget"
)

func main() {
	defaults := config.Defaults()
	configPath := flag.String("config", "", "config file path (default: eink-server.toml beside executable)")
	deviceAddr := flag.String("device-listen", defaults.DeviceListen, "override tablet TCP listen address")
	httpAddr := flag.String("http-listen", defaults.HTTPListen, "override management HTTP listen address")
	database := flag.String("database", defaults.Database, "override SQLite database path")
	logFormat := flag.String("log-format", defaults.LogFormat, "override log format: text or json")
	flag.Parse()

	explicitConfig := *configPath != ""
	if !explicitConfig {
		var err error
		*configPath, err = config.DefaultPath()
		if err != nil {
			fatal(slog.Default(), "resolving default config path", err)
		}
	}
	cfg, err := config.Load(*configPath, explicitConfig)
	if err != nil {
		fatal(slog.Default(), "loading config", err)
	}
	flag.Visit(func(value *flag.Flag) {
		switch value.Name {
		case "device-listen":
			cfg.DeviceListen = *deviceAddr
		case "http-listen":
			cfg.HTTPListen = *httpAddr
		case "database":
			cfg.Database = *database
		case "log-format":
			cfg.LogFormat = *logFormat
		}
	})
	if err := cfg.Validate(); err != nil {
		fatal(slog.Default(), "validating config", err)
	}
	*deviceAddr, *httpAddr, *database, *logFormat = cfg.DeviceListen, cfg.HTTPListen, cfg.Database, cfg.LogFormat

	var handler slog.Handler
	if *logFormat == "json" {
		handler = slog.NewJSONHandler(os.Stdout, nil)
	} else {
		handler = slog.NewTextHandler(os.Stdout, nil)
	}
	log := slog.New(handler)
	slog.SetDefault(log)
	if err := os.MkdirAll(filepath.Dir(*database), 0750); err != nil {
		fatal(log, "creating data directory", err)
	}
	db, err := store.Open(*database)
	if err != nil {
		fatal(log, "opening database", err)
	}
	defer db.Close()
	defaultSettings := imageproc.Defaults()
	defaultSettings.Rendering = cfg.DefaultRendering
	db.DefaultSettings = defaultSettings
	db.DefaultTimezone = cfg.DefaultTimezone
	db.DefaultLocale = cfg.DefaultLocale
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	hub := events.New()
	gw := gateway.New(db, hub, log)
	fontCleanup, err := design.ConfigureFonts(cfg.FontDirectory, cfg.UseSystemFonts)
	if err != nil {
		fatal(log, "loading fonts", err)
	}
	defer fontCleanup()
	var configuredActions []store.Action
	for name, definition := range cfg.Actions {
		configuredActions = append(configuredActions, store.Action{Name: name, Source: "config", Kind: "webhook", URL: definition.URL, Headers: definition.Headers, TimeoutMS: int(definition.TimeoutDuration() / time.Millisecond)})
	}
	if err := db.ReconcileConfigActions(ctx, configuredActions); err != nil {
		fatal(log, "loading configured actions", err)
	}
	runner := action.New(ctx, db, hub, log)
	widgetRuntime, err := widget.New(ctx, filepath.Dir(*configPath), cfg.Widgets, map[string][]byte{"departures": widget.DeparturesWASM})
	if err != nil {
		fatal(log, "loading widgets", err)
	}
	defer widgetRuntime.Close(context.Background())
	designs := &design.Service{Store: db, Hub: hub, Log: log, Actions: runner, Notifier: gw, Connections: gw, SystemName: cfg.SystemName, DesignDirectory: cfg.DesignDirectory, DefaultDesign: cfg.DefaultDesign, Widgets: widgetRuntime}
	if err := designs.Init(ctx); err != nil {
		fatal(log, "loading designs", err)
	}
	gw.Designs = designs
	go designs.RunScheduler(ctx)
	api := &httpapi.API{Store: db, Hub: hub, Connections: gw, Designs: designs, Log: log}
	httpServer := &http.Server{Addr: *httpAddr, Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	errs := make(chan error, 2)
	go func() { errs <- gw.Serve(ctx, *deviceAddr) }()
	go func() {
		log.Info("management server listening", "address", *httpAddr)
		err := httpServer.ListenAndServe()
		if err == http.ErrServerClosed {
			err = nil
		}
		errs <- err
	}()
	select {
	case err := <-errs:
		if err != nil {
			log.Error("server stopped", "error", err)
			cancel()
		}
	case <-ctx.Done():
	}
	shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	_ = httpServer.Shutdown(shutdownCtx)
}

func fatal(log *slog.Logger, message string, err error) {
	log.Error(message, "error", err)
	fmt.Fprintln(os.Stderr, message+":", err)
	os.Exit(1)
}
