# Bifrost Bench. Run from bench/, or from the repo root as `make bench-<target>`.
#
#   make up        build everything, create deploy/.env if missing, start Bifrost, Postgres and the fault proxy
#   make smoke     a no-key end-to-end run: toy agent, scripted model, clean and model-outage
#   make serve     the web UI and API on http://127.0.0.1:8090
#   make down      stop the stack and remove its volumes

GO      ?= go
GOENV   := GOWORK=off
BIN     := bin/bifrost-bench
COMPOSE := docker compose -f deploy/docker-compose.yml --env-file deploy/.env
WITHENV := set -a; . deploy/.env; set +a;

.PHONY: help build bin ui test test-integration images env up down admit doctor smoke serve demo clean

help: ## Show targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

bin: ## Build the bench binary (static, for the host and the bench image)
	$(GOENV) CGO_ENABLED=0 $(GO) build -o $(BIN) ./cmd/bifrost-bench

ui: ## Build the web UI into ui/dist (embedded by the binary)
	cd ui && npm ci --no-audit --no-fund && npm run build

build: ui bin ## Build the UI, then the binary with the UI embedded

test: ## go vet and unit tests (no Docker, no network)
	$(GOENV) $(GO) vet ./...
	$(GOENV) $(GO) test ./...

test-integration: ## Tests that use real Docker
	$(GOENV) $(GO) test -tags integration ./environments/ ./graders/ ./probes/ ./gateways/...

images: bin ## Build the task base image, the bench image and the agent images
	docker build -t bench/coding-core-base:1 benchmarks/coding-core/base
	docker build -t bench/bifrost-bench:dev .
	docker build -t bench/agent-claude-code:1 agents/images/claude-code

env: ## Create deploy/.env with random tokens if it does not exist
	@if [ ! -f deploy/.env ]; then \
		sed -e "s#^BIFROST_SETUP_TOKEN=.*#BIFROST_SETUP_TOKEN=$$(openssl rand -hex 24)#" \
		    -e "s#^BIFROST_PG_PASSWORD=.*#BIFROST_PG_PASSWORD=$$(openssl rand -hex 24)#" \
		    -e "s#^BENCH_FAULTPROXY_TOKEN=.*#BENCH_FAULTPROXY_TOKEN=$$(openssl rand -hex 24)#" \
		    deploy/.env.example > deploy/.env; \
		chmod 600 deploy/.env; \
		echo "created deploy/.env (scripted model; set ANTHROPIC_API_KEY there for real runs)"; \
	fi

up: env images ## Start the gateway stack
	$(COMPOSE) up -d --wait

down: ## Stop the stack and remove its volumes
	$(COMPOSE) down -v

admit: bin ## Run admission for every task and write suite.lock
	./$(BIN) suite admit coding-core

doctor: bin ## Preflight checks
	@$(WITHENV) ./$(BIN) doctor

smoke: bin ## No-key end-to-end run through real Bifrost
	@$(WITHENV) ./$(BIN) run experiments/smoke.yaml

serve: bin ## Web UI and API
	@$(WITHENV) ./$(BIN) serve

demo: up smoke serve ## Start everything, run the smoke experiment, then serve the UI

clean: ## Remove build outputs
	rm -rf bin ui/dist/assets ui/dist/index.html
