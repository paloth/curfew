package logs

import (
	"log/slog"
	"os"
)

// Logger is the global logger instance
var Logger *slog.Logger

// InitLogger initializes the global logger with JSON formatting
// and sets the log level based on the debug flag
func InitLogger(debug bool) {
	logLevel := slog.LevelInfo
	if debug {
		logLevel = slog.LevelDebug
	}

	params := &slog.HandlerOptions{
		Level: logLevel,
	}

	Logger = slog.New(slog.NewJSONHandler(os.Stdout, params))

	slog.SetDefault(Logger)

	if debug {
		Logger.Info("Debug mode enabled")
	}
}
