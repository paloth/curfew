package cmd

import (
	"context"
	"os"

	"curfew/internal/curfew"
	"curfew/internal/logs"
	"curfew/internal/telemetry"

	"github.com/spf13/cobra"
)

var (
	rootCmd = &cobra.Command{
		Use:   "curfew",
		Short: "Curfew is a Kubernetes operator that automatically scales workloads (Deployments and StatefulSets) based on schedule annotations.",
		Run: func(cmd *cobra.Command, args []string) {
			logs.InitLogger(dev)

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
		},
	}

	dev bool

	// Version is set at build time with -ldflags "-X curfew/cmd.Version=...".
	Version = "dev"
)

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.Flags().BoolVar(&dev, "dev", false, "Enable development mode (debug logs)")
}
