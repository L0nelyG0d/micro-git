# micro git: set up, run, test and stop the whole project.
# Works with GNU make 3.81 (macOS) and 4.x. Run `make` or `make help`.
#
# Secrets: .env is never sourced (it holds unquoted & in connection strings).
# Each variable is read with grep/cut (text after the first =) and passed to the
# one process that needs it. Values are never echoed.

.DEFAULT_GOAL := help
.PHONY: help setup check-env schema backend frontend dev test test-fast test-all build cli lint clean \
	run-sqlite run-memory run-pg dev-secret clean-data dev-sqlite smoke-sqlite

# Backend port; override with `make dev PORT=9090`.
PORT ?= 8080
# Vite dev server port; override with `make frontend VITE_PORT=5174`.
VITE_PORT := 5173

# Throwaway local Postgres from _docs/AGENTS.md; `make test` falls back to it.
# No secrets: the role has no password.
LOCAL_PG_PORT := 54329
LOCAL_TEST_DB := postgres://test@localhost:$(LOCAL_PG_PORT)/mg?sslmode=disable

# SQLite data directory owned by run-sqlite and clean-data (git-ignored data/).
DATA_DIR := $(CURDIR)/data
SQLITE_FILE := $(DATA_DIR)/mg.db

# $(call envval,NAME) -> shell command printing the value of NAME from .env.
envval = grep '^$(1)=' .env | head -n 1 | cut -d= -f2- | tr -d '\r'

# Shell snippet: exit 1 with a hint if TCP port $(1) has a listener; $(2) is the
# make target named in the hint.
portcheck = busy=; \
	if command -v lsof >/dev/null 2>&1; then \
		if lsof -nP -iTCP:$(1) -sTCP:LISTEN >/dev/null 2>&1; then busy=1; fi; \
	elif nc -z localhost $(1) >/dev/null 2>&1; then busy=1; fi; \
	if [ -n "$$busy" ]; then echo "port $(1) is busy; run: make $(2) $(or $(3),PORT)=<other>"; exit 1; fi

# Shell snippet: export the backend variables from .env (values never printed).
loadenv = export DATABASE_URL="$$($(call envval,DATABASE_URL))" \
		JWT_SECRET="$$($(call envval,JWT_SECRET))" \
		COOKIE_SECURE="$$($(call envval,COOKIE_SECURE))" \
		PORT="$(PORT)"

cli: ## Build the command-line client to bin/mg (see README, "Command line")
	cd backend && go build -o ../bin/mg ./cmd/mg
	@echo "built bin/mg  (try: bin/mg help)"

help: ## Show this help (default target)
	@echo "Targets (PORT=$(PORT); override: make dev PORT=9090):"
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | \
		awk -F ':.*## ' '{ printf "  %-12s %s\n", $$1, $$2 }'

setup: ## Install dependencies (go mod download, npm ci)
	cd backend && go mod download
	cd frontend && npm ci

check-env: ## Verify .env has DATABASE_URL and a JWT_SECRET of 32+ bytes (never prints values)
	@if [ ! -f .env ]; then echo "check-env: .env not found in the repository root"; exit 1; fi
	@v="$$($(call envval,DATABASE_URL))"; \
	if [ -z "$$v" ]; then echo "check-env: DATABASE_URL is missing or empty in .env"; exit 1; fi; \
	v="$$($(call envval,JWT_SECRET))"; \
	if [ -z "$$v" ]; then echo "check-env: JWT_SECRET is missing or empty in .env"; exit 1; fi; \
	n=$$(printf %s "$$v" | wc -c | tr -d ' '); \
	if [ "$$n" -lt 32 ]; then echo "check-env: JWT_SECRET is shorter than 32 bytes"; exit 1; fi; \
	echo "check-env: ok"

schema: check-env ## Apply backend/db/schema.sql to DATABASE_URL (5 tries, Neon cold start)
	@export DATABASE_URL="$$($(call envval,DATABASE_URL))" PGCONNECT_TIMEOUT=15; \
	i=1; \
	while [ $$i -le 5 ]; do \
		if psql -v ON_ERROR_STOP=1 -q "$$DATABASE_URL" -f backend/db/schema.sql >/dev/null 2>&1; then \
			echo "schema: applied"; exit 0; \
		fi; \
		echo "schema: attempt $$i of 5 failed"; \
		if [ $$i -lt 5 ]; then sleep 3; fi; \
		i=$$((i + 1)); \
	done; \
	echo "schema: could not apply backend/db/schema.sql after 5 attempts (check DATABASE_URL and the database)"; exit 1

backend: check-env ## Run the Go server on PORT (default 8080) with variables from .env
	@$(call portcheck,$(PORT),backend)
	@$(loadenv); cd backend && exec go run .

frontend: ## Run the Vite dev server (proxies /api to http://localhost:PORT)
	@$(call portcheck,$(VITE_PORT),frontend,VITE_PORT)
	@cd frontend && API_PROXY_TARGET="http://localhost:$(PORT)" exec npm run dev -- --port $(VITE_PORT) --strictPort

dev: check-env ## Run backend and frontend together; Ctrl-C stops both
	@$(call portcheck,$(PORT),dev)
	@$(call portcheck,$(VITE_PORT),dev,VITE_PORT)
	@set -m; \
	$(loadenv); \
	( cd backend && exec go run . ) & bpid=$$!; \
	( cd frontend && API_PROXY_TARGET="http://localhost:$(PORT)" exec npm run dev -- --port $(VITE_PORT) --strictPort ) & fpid=$$!; \
	stop() { \
		trap '' INT TERM; \
		kill -TERM -$$bpid 2>/dev/null; kill -TERM -$$fpid 2>/dev/null; \
		i=0; while [ $$i -lt 20 ] && { kill -0 $$bpid 2>/dev/null || kill -0 $$fpid 2>/dev/null; }; do sleep 0.25; i=$$((i + 1)); done; \
		kill -KILL -$$bpid 2>/dev/null; kill -KILL -$$fpid 2>/dev/null; \
		wait 2>/dev/null; \
	}; \
	trap 'stop; exit 130' INT TERM; \
	trap 'stop' EXIT; \
	echo "dev: backend on :$(PORT), frontend on http://localhost:$(VITE_PORT) (Ctrl-C stops both)"; \
	while kill -0 $$bpid 2>/dev/null && kill -0 $$fpid 2>/dev/null; do sleep 1; done; \
	echo "dev: a process exited; stopping the other"; exit 1

test: ## Whole suite: backend, then frontend. DB: TEST_DATABASE_URL, else local Postgres :54329, else .env
	@if [ -z "$$TEST_DATABASE_URL" ] && pg_isready -q -h localhost -p $(LOCAL_PG_PORT) >/dev/null 2>&1; then \
		export TEST_DATABASE_URL="$(LOCAL_TEST_DB)"; \
		echo "test: using the local Postgres on localhost:$(LOCAL_PG_PORT) (TEST_DATABASE_URL was unset)"; \
	elif [ -n "$$TEST_DATABASE_URL" ]; then \
		echo "test: using TEST_DATABASE_URL from the environment"; \
	elif [ -f .env ] && [ -n "$$($(call envval,TEST_DATABASE_URL))" ]; then \
		export TEST_DATABASE_URL="$$($(call envval,TEST_DATABASE_URL))"; \
		echo "test: using TEST_DATABASE_URL from .env"; \
	else \
		echo "test: no test database (TEST_DATABASE_URL unset, local Postgres on :$(LOCAL_PG_PORT) not reachable); database tests will be skipped"; \
	fi; \
	if [ -z "$$JWT_SECRET" ] && [ -f .env ]; then export JWT_SECRET="$$($(call envval,JWT_SECRET))"; fi; \
	cd backend && go test -p 1 -race -timeout 40m ./...
	cd frontend && npm test -- --run

test-fast: ## Quick tests without a database: pure Go packages and frontend
	cd backend && go test ./gitobj/ ./treebuild/ ./linediff/
	cd frontend && npm test -- --run

build: ## Build backend/backend and the frontend (npm run build)
	cd backend && go build -o backend .
	cd frontend && npm run build

lint: ## gofmt -l (must list nothing), go vet, tsc -b, npm run lint if defined
	@out="$$(cd backend && gofmt -l .)"; \
	if [ -n "$$out" ]; then echo "gofmt: these files need formatting:"; echo "$$out"; exit 1; fi
	cd backend && go vet ./...
	cd frontend && npx --no-install tsc -b
	@if grep -q '"lint"' frontend/package.json; then cd frontend && npm run lint; fi

# Shell snippet: set JWT_SECRET from .env (made by dev-secret) and PORT, plus the
# extra variables in $(1). The secret is never echoed.
runenv = export JWT_SECRET="$$($(call envval,JWT_SECRET))" PORT="$(PORT)" $(1)

dev-secret: ## Create .env with a random JWT_SECRET if it is missing (never printed)
	@if [ -f .env ] && [ -n "$$($(call envval,JWT_SECRET))" ]; then exit 0; fi; \
	if [ -f .env ]; then grep -v '^JWT_SECRET=' .env > .env.tmp || true; mv .env.tmp .env; \
		if [ -s .env ] && [ -n "$$(tail -c 1 .env)" ]; then echo >> .env; fi; fi; \
	if command -v openssl >/dev/null 2>&1; then s="$$(openssl rand -hex 32)"; \
	else s="$$(LC_ALL=C head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"; fi; \
	if [ "$${#s}" -lt 32 ]; then echo "dev-secret: could not generate a secret"; exit 1; fi; \
	printf 'JWT_SECRET=%s\n' "$$s" >> .env; chmod 600 .env; \
	echo "dev-secret: wrote a new JWT_SECRET to .env (git-ignored, not shown)"

run-sqlite: dev-secret ## Run the backend on SQLite (data/mg.db), no Postgres needed
	@$(call portcheck,$(PORT),run-sqlite)
	@mkdir -p "$(DATA_DIR)"
	@$(call runenv,DB_DRIVER=sqlite SQLITE_PATH="$(SQLITE_FILE)"); cd backend && exec go run .

dev-sqlite: dev-secret ## Run backend (SQLite, data/mg.db) and frontend together; no Postgres; Ctrl-C stops both
	@$(call portcheck,$(PORT),dev-sqlite)
	@$(call portcheck,$(VITE_PORT),dev-sqlite,VITE_PORT)
	@mkdir -p "$(DATA_DIR)"
	@set -m; \
	$(call runenv,DB_DRIVER=sqlite SQLITE_PATH="$(SQLITE_FILE)"); \
	( cd backend && exec go run . ) & bpid=$$!; \
	( cd frontend && unset VITE_USE_MOCK; API_PROXY_TARGET="http://localhost:$(PORT)" exec npm run dev -- --port $(VITE_PORT) --strictPort ) & fpid=$$!; \
	stop() { \
		trap '' INT TERM; \
		kill -TERM -$$bpid 2>/dev/null; kill -TERM -$$fpid 2>/dev/null; \
		i=0; while [ $$i -lt 20 ] && { kill -0 $$bpid 2>/dev/null || kill -0 $$fpid 2>/dev/null; }; do sleep 0.25; i=$$((i + 1)); done; \
		kill -KILL -$$bpid 2>/dev/null; kill -KILL -$$fpid 2>/dev/null; \
		wait 2>/dev/null; \
	}; \
	trap 'stop; exit 130' INT TERM; \
	trap 'stop' EXIT; \
	echo "dev-sqlite: backend on :$(PORT) (data/mg.db), frontend on http://localhost:$(VITE_PORT) (Ctrl-C stops both)"; \
	while kill -0 $$bpid 2>/dev/null && kill -0 $$fpid 2>/dev/null; do sleep 1; done; \
	echo "dev-sqlite: a process exited; stopping the other"; exit 1

smoke-sqlite: ## Integration smoke: real frontend (Vite proxy) + real backend on a temp SQLite file, free ports
	@scripts/smoke-sqlite.sh

run-memory: dev-secret ## Run the backend with the in-memory store (data is lost on exit)
	@$(call portcheck,$(PORT),run-memory)
	@$(call runenv,STORE=memory); cd backend && exec go run .

run-pg: check-env ## Run the backend on Postgres (DATABASE_URL from .env; same as backend)
	@$(call portcheck,$(PORT),run-pg)
	@$(loadenv) DB_DRIVER=postgres; cd backend && exec go run .

test-all: ## Backend on memory, sqlite, postgres (scripts/test-matrix.sh, isolated DB), then the frontend
	scripts/test-matrix.sh
	cd frontend && npm test -- --run

clean-data: ## Delete the SQLite files in data/ (mg.db, -wal, -shm) and nothing else
	@d="$(DATA_DIR)"; \
	case "$$d" in ""|"/"|"$$HOME"|"$$HOME/") echo "clean-data: unsafe path '$$d'"; exit 1;; esac; \
	if [ "$$(basename "$$d")" != "data" ] || [ ! -f "$(CURDIR)/Makefile" ]; then echo "clean-data: refusing '$$d'"; exit 1; fi; \
	if [ -L "$$d" ]; then echo "clean-data: $$d is a symlink; refusing"; exit 1; fi; \
	if [ ! -d "$$d" ]; then echo "clean-data: nothing to remove"; exit 0; fi; \
	for f in mg.db mg.db-wal mg.db-shm mg.db-journal; do \
		if [ -f "$$d/$$f" ] && [ ! -L "$$d/$$f" ]; then rm -f "$$d/$$f"; echo "clean-data: removed data/$$f"; fi; \
	done; \
	rmdir "$$d" 2>/dev/null || true

clean: ## Remove backend/backend and frontend/dist (keeps .env and node_modules)
	rm -rf backend/backend bin frontend/dist
