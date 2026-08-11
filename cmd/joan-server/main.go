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

	"joantablet/server/internal/events"
	"joantablet/server/internal/gateway"
	"joantablet/server/internal/httpapi"
	"joantablet/server/internal/store"
)

func main() {
	deviceAddr := flag.String("device-listen", ":11113", "tablet TCP listen address")
	httpAddr := flag.String("http-listen", ":8080", "management HTTP listen address")
	database := flag.String("database", "./data/joan.db", "SQLite database path")
	logFormat := flag.String("log-format", "text", "text or json")
	flag.Parse()
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
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	hub := events.New()
	gw := gateway.New(db, hub, log)
	api := &httpapi.API{Store: db, Hub: hub, Connections: gw, Log: log}
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
