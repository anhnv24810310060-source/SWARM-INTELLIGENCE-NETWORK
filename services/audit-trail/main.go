package main

import (
	"log/slog"

	sloglog "github.com/swarmguard/libs/go/core/logging"
)

func main() {
	sloglog.Init("audit-trail")
	slog.Info("starting service")
	// TODO: Append-only log & Merkle root chain
}
