module github.com/swarmguard/audit-trail

go 1.22

require (
	github.com/swarmguard/libs/go/core v0.0.0
	go.opentelemetry.io/otel v1.28.0 // indirect for meter instrumentation
)

replace github.com/swarmguard/libs/go/core => ../../libs/go/core
