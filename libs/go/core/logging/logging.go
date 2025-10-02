package logging

import (
	"log/slog"
	"os"
	"strings"
)

// Init configures a global slog logger. JSON if SWARM_JSON_LOG=1/true else text.
func Init(service string) *slog.Logger {
	mode := strings.ToLower(os.Getenv("SWARM_JSON_LOG"))
	var handler slog.Handler
	if mode == "1" || mode == "true" || mode == "json" {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{AddSource: false, Level: levelFromEnv()})
	} else {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{AddSource: false, Level: levelFromEnv()})
	}
	logger := slog.New(handler).With("service", service)
	slog.SetDefault(logger)
	logger.Info("logging initialized", "json", (mode == "1" || mode == "true" || mode == "json"))
	return logger
}

func levelFromEnv() slog.Leveler {
	lvl := strings.ToLower(os.Getenv("SWARM_LOG_LEVEL"))
	switch lvl {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "info", "":
		return slog.LevelInfo
	default:
		return slog.LevelInfo
	}
}
