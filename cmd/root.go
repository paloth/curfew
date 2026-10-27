package cmd

import (
	"context"
	"flag"
	"os"

	"curfew/internal/curfew"
	"curfew/internal/logs"
	"curfew/internal/telemetry"
)

// Version is set at build time with -ldflags "-X curfew/cmd.Version=...".
var Version = "dev"

// Execute parses the flags, sets up logging and tracing, then runs the operator until it stops.
func Execute() {
	dev := flag.Bool("dev", false, "Enable development mode (debug logs)")
	flag.Parse()
	logs.InitLogger(*dev)

	// Tracing is opt-in: without an endpoint the global tracer is a no-op.
	shutdownTracer := func(context.Context) error { return nil }
	if endpoint := os.Getenv("JAEGER_ENDPOINT"); endpoint != "" {
		var err error
		if shutdownTracer, err = telemetry.InitTracer("curfew", Version, endpoint); err != nil {
			logs.Logger.Error("Failed to initialize tracing", "error", err)
			os.Exit(1)
		}
		logs.Logger.Info("Tracing enabled", "endpoint", endpoint)
	}

	logs.Logger.Info("Starting curfew ...", "version", Version)
	err := curfew.Run()
	if shutdownErr := shutdownTracer(context.Background()); shutdownErr != nil {
		logs.Logger.Error("Failed to flush traces", "error", shutdownErr)
	}
	if err != nil {
		logs.Logger.Error("curfew stopped", "error", err)
		os.Exit(1)
	}
}
