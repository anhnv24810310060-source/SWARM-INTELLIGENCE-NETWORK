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
	@echo "[TEST][RUST] running cargo tests with coverage (if tarpaulin installed)"
	command -v cargo-tarpaulin >/dev/null 2>&1 && cargo tarpaulin -q --workspace --out Xml || echo "tarpaulin not installed" 
	@echo "[TEST][GO] running go test -cover (summary)"
	@for svc in $(GO_SERVICES); do \
		if [ -f services/$$svc/go.mod ]; then \
			( cd services/$$svc && go test ./... -coverprofile=coverage.out >/dev/null 2>&1 || true ); \
		fi; \
	done
	@echo "[TEST][PY] pytest coverage (placeholder)"

format:
	@echo "(placeholder) run formatters"

security: security-cargo-audit security-govulncheck security-pip-audit
	@echo "[SECURITY] aggregate scan complete"
	@echo "[SECURITY] detect-secrets scan"
	@command -v detect-secrets >/dev/null 2>&1 && detect-secrets scan || echo "detect-secrets not installed"
	@echo "[SECURITY] checkov scan (infra)"
	@command -v checkov >/dev/null 2>&1 && checkov -d infra || echo "checkov not installed"

cosign-sign:
	@echo "(placeholder) sign container images with cosign" 
	@echo "Usage: make cosign-sign IMAGE=repo/name:tag" 
	@[ -z "$(IMAGE)" ] && echo "Set IMAGE var" || echo "Would run: cosign sign $(IMAGE)"

coverage-threshold:
	@echo "(placeholder) enforce coverage thresholds" 
	@echo "Implement parsing of coverage outputs and fail if below env THRESHOLD"

perf-ingest:
	@echo "(placeholder) run ingestion throughput benchmark"
	@echo "Would execute benches or script in future"

resilience-test:
	@echo "(placeholder) run resilience tests (circuit breaker simulation)"

security-cargo-audit:
	@command -v cargo-audit >/dev/null 2>&1 || { echo "Installing cargo-audit"; cargo install cargo-audit >/dev/null 2>&1 || true; }
	@echo "[SECURITY][cargo-audit] scanning workspace" && cargo audit || true

security-govulncheck:
	@command -v govulncheck >/dev/null 2>&1 || { echo "Installing govulncheck"; go install golang.org/x/vuln/cmd/govulncheck@latest >/dev/null 2>&1 || true; }
	@for svc in $(GO_SERVICES); do \
		if [ -f services/$$svc/go.mod ]; then \
			echo "[SECURITY][govulncheck] $$svc"; \
			( cd services/$$svc && govulncheck ./... || true ); \
		fi; \
	done

security-pip-audit:
	@command -v pip-audit >/dev/null 2>&1 || { echo "Installing pip-audit"; pip install --user pip-audit >/dev/null 2>&1 || true; }
	@for svc in $(PY_SERVICES); do \
		if [ -f services/$$svc/pyproject.toml ]; then \
			echo "[SECURITY][pip-audit] $$svc"; \
			( cd services/$$svc && pip-audit || true ); \
		fi; \
	done

sbom:
	@echo "(placeholder) build image & run syft-sbom.sh <image>"

license-check:
	bash scripts/check-license.sh || true

dev-up:
	@docker compose -f infra/docker-compose.dev.yml up -d

dev-down:
	@docker compose -f infra/docker-compose.dev.yml down -v
