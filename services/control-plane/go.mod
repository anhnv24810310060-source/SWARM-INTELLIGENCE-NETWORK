module github.com/swarmguard/control-plane

go 1.22

require (
	google.golang.org/grpc v1.65.0
	github.com/nats-io/nats.go v1.33.1
)

replace github.com/swarmguard/proto/gen/go => ../../proto/gen/go
