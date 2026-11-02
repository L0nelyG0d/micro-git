# syntax=docker/dockerfile:1

# Stage 1: build the React frontend into frontend/dist.
FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# Stage 2: build the Go API server (backend/main.go, package main).
FROM golang:1.26-alpine AS backend
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mg-server .

# Stage 3: runtime. Binary plus static frontend, no toolchain, non-root user.
FROM alpine:3.20
RUN addgroup -S app && adduser -S -G app -h /app app \
 && mkdir -p /app/public \
 && chown app:app /app
WORKDIR /app
COPY --from=backend /out/mg-server /app/mg-server
COPY --from=frontend /src/frontend/dist /app/public

ENV PORT=8080 \
    STATIC_DIR=/app/public \
    DB_DRIVER=postgres \
    STORE=db
USER app
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -q -O /dev/null "http://127.0.0.1:${PORT}/health" || exit 1

ENTRYPOINT ["/app/mg-server"]
