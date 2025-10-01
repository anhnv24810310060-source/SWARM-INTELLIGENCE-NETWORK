package main

import (
	"log/slog"

	sloglog "github.com/swarmguard/libs/go/core/logging"
)

func main() {
	sloglog.Init("threat-intel")
	slog.Info("starting service")
	// TODO: IOC ingest + reputation cache
}
