package main

import (
	"log/slog"

	sloglog "github.com/swarmguard/libs/go/core/logging"
)

func main() {
	sloglog.Init("policy-service")
	slog.Info("starting service")
	// TODO: gRPC server + policy CRUD + version store
}
