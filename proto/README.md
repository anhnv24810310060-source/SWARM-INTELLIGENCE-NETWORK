# Proto Definitions

Structure (tạm thời):
- common/: message chung (trace id, pagination, error)
- health/: healthcheck service
- events/: security & telemetry event schema
- consensus/: messages cho PBFT
- federation/: federated learning rounds

Sinh code sẽ đặt trong `proto/gen/<lang>/...`
