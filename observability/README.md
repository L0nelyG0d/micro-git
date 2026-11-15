# Observability (Zoomcamp module 4)

Stack: OTel Collector (contrib 0.111.0) -> Prometheus (metrics), Tempo (traces), Loki (logs); Alertmanager; Grafana.

## Run (from repo root)
    incident-response/make-token.sh          # first: creates incident-response/.alert-token (Alertmanager webhook bearer token)
    export GRAFANA_ADMIN_PASSWORD='choose-one'   # required, no default
    docker compose -f docker-compose.yml -f observability/compose.yaml -f observability/app-telemetry.yaml up -d --build

`.alert-token` must exist before `up` (it is bind-mounted read-only into Alertmanager); an empty placeholder is not acceptable, the responder must accept the same token. All published ports (Grafana 3000, Prometheus 9090, collector 4318, Loki 3100, Tempo 3200) are bound to 127.0.0.1. Prometheus runs the remote-write receiver without auth, so keep 9090 on loopback.

Merging puts everything on the root compose default network, so the collector reaches `app:8080`.
Grafana http://localhost:3000 (admin / `$GRAFANA_ADMIN_PASSWORD`, required), Prometheus http://localhost:9090.
For traces the app needs `OTEL_EXPORTER_OTLP_ENDPOINT=http://collector:4318`; the override `observability/app-telemetry.yaml` sets it.

## Data flow
- Metrics: collector `prometheus` receiver scrapes `app:8080/metrics` (job `micro-git`) -> Prometheus remote write.
- Traces: app OTLP/HTTP -> collector -> Tempo (gRPC).
- Logs: collector `filelog` tails Docker json-file logs (`/var/lib/docker/containers`, mounted read-only, collector runs as root), keeps only lines containing `"route"` (the app's request logs), labels `service.name=micro-git`, sends to Loki OTLP. Chosen because it needs no app change or log driver plugin. Works on Docker Desktop and Linux; not on rootless Docker/podman.
- Grafana Loki datasource has a derived field: `trace_id` in a log line becomes a "View trace" link to Tempo.

## Environments
This stack is local. The dev (Render) and prod (AWS) instances label their own telemetry with `APP_ENV` (`dev`, `prod`; `local` by default): field `env` in the JSON logs, label `env` on `build_info`, resource attribute `deployment.environment` on spans. In this stack the collector adds `env=local` to every scraped series (`honor_labels: true` keeps the app's own value on `build_info`), the dashboard has an `env` variable used by every panel query, and the alert rules aggregate `by (env)` so an alert says which environment fired. Logs are not indexed by `env`; filter with `| json | env="prod"`. To observe a hosted environment, point a collector at it (scrape target and OTLP endpoint); nothing here reaches Render or AWS.

## Alerts (alerts.yaml)
HighErrorRatioOnRoute (5xx > 5% for 2m, min ~3 req/min), P95LatencyHigh (p95 > 1s for 5m, same min traffic), AppDown (`up==0` for 1m). Alertmanager POSTs to `http://host.docker.internal:9099/alert` (the local responder) with a Bearer token from `/etc/alertmanager/alert-token`. `extra_hosts: host-gateway` maps the name on Linux, but the responder must listen on an address reachable from the Docker bridge, not only 127.0.0.1.

## /metrics exposure
By default (METRICS_ADDR unset, COOKIE_SECURE not `true`) `/metrics` is on the public app port (the collector scrapes `app:8080/metrics`). It reveals routes, traffic and version. With `COOKIE_SECURE=true` and no `METRICS_ADDR`, `/metrics` is disabled (WARN logged). In production set `METRICS_ADDR` (e.g. `:9100`): `/metrics` is then served only on that listener and removed from the public port; point the collector scrape target at `app:9100` and do not publish that port. `METRICS_ADDR` must not use the same port as `PORT` (startup is refused). In `docker-compose.yml` it is passed through from the environment.

**Gotcha:** with `COOKIE_SECURE=true` and `METRICS_ADDR` unset, the collector's scrape of `app:8080/metrics` gets 404 and the `AppDown` alert fires after 1m. Set `METRICS_ADDR` (e.g. `0.0.0.0:9100` in the container) and change the target in `collector.yaml` to `app:9100`.

## Gotchas
- Dashboard trace panel and log filter use the `trace_id` textbox variable; default `.*` shows all logs, and the trace panel stays empty/errors until a real id is pasted (or use the log "View trace" link).
- If the log filter drops everything, check that the app's JSON logs contain a `route` field.
