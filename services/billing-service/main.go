package main

import (
	"log/slog"

	sloglog "github.com/swarmguard/libs/go/core/logging"
)

func main() {
	sloglog.Init("billing-service")
	slog.Info("starting service")
	// TODO: Usage aggregation + pricing engine
}
