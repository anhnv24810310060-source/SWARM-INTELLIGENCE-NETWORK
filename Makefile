# Root Makefile orchestrating multi-language build

RUST_SERVICES = sensor-gateway node-runtime swarm-gossip consensus-core identity-ca inference-gateway risk-engine edge-fleet
GO_SERVICES = policy-service control-plane billing-service audit-trail threat-intel
PY_SERVICES = model-registry federated-orchestrator evolution-core

.PHONY: all rust go python proto test format security proto-clean

all: proto rust go python

proto:
	@echo "[PROTO] generate via buf"
	bash scripts/generate-proto.sh

proto-clean:
	rm -rf proto/gen || true

rust:
	@for svc in $(RUST_SERVICES); do \
	  echo "[BUILD][RUST] $$svc"; \
	  if [ -f services/$$svc/Cargo.toml ]; then \
	    (cd services/$$svc && cargo build --quiet); \
	  fi; \
	done

go:
	@for svc in $(GO_SERVICES); do \
	  echo "[BUILD][GO] $$svc"; \
	  if [ -f services/$$svc/go.mod ]; then \
	    (cd services/$$svc && go build ./... >/dev/null); \
	  fi; \
	done

python:
	@for svc in $(PY_SERVICES); do \
	  echo "[CHECK][PY] $$svc"; \
	  if [ -f services/$$svc/pyproject.toml ]; then \
	    (cd services/$$svc && python -m pyproject_hooks build >/dev/null 2>&1 || true); \
	  fi; \
	done

test:
	@echo "(placeholder) aggregate tests"

format:
	@echo "(placeholder) run formatters"

security:
	@echo "(placeholder) run security scans (trivy, cargo audit, osv-scanner)"

sbom:
	@echo "(placeholder) build image & run syft-sbom.sh <image>"

license-check:
	bash scripts/check-license.sh || true

dev-up:
	@docker compose -f infra/docker-compose.dev.yml up -d

dev-down:
	@docker compose -f infra/docker-compose.dev.yml down -v
