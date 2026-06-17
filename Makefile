.PHONY: dev build build-web build-api build-frontend build-ssr clean test

# Start both servers for local development.
# Requires: Go 1.23+, Node 20+
# The web server proxies /api/* to port 8080.
dev:
	@echo "Starting API on :8080 and web on :3000 ..."
	@(cd frontend && npm run dev &) && \
	 go run ./cmd/api & \
	 go run ./cmd/web

# Build the full web binary (requires frontend dist to exist).
# Use build-web for a complete build including frontend.
build:
	go build -o bin/app-web ./cmd/web
	go build -o bin/app-api ./cmd/api

# Full web build: frontend → dist → embed → Go binary
build-web:
	cd frontend && npm install && npm run build && npm run build:ssr
	cp -r frontend/dist static/dist/
	go build -o bin/app-web ./cmd/web

build-api:
	go build -o bin/app-api ./cmd/api

# Build frontend only (client SPA)
build-frontend:
	cd frontend && npm install && npm run build

# Build SSR bundle only
build-ssr:
	cd frontend && npm run build:ssr

# Run Go tests
test:
	go test -race -count=1 ./...

# Remove build artifacts
clean:
	rm -f bin/app-web bin/app-api
	rm -rf frontend/dist frontend/dist-ssr
	find static/dist -not -name '.gitkeep' -delete 2>/dev/null || true
