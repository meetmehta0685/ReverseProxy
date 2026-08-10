# Reverse Proxy

A small, production-minded HTTP reverse proxy written in Go's standard library. It is intentionally narrow: one configured upstream, one binary, no external services, and explicit operational behavior.

The project is also a learning artifact. The `lessons/`, `reference/`, and `learning-records/` directories explain how the implementation works from first principles.

## What it demonstrates

- HTTP forwarding with `net/http/httputil.ReverseProxy`
- Request deadlines, connection pooling, graceful shutdown, and bounded upstream connections
- A process-local fixed-window rate limiter
- A circuit breaker with a single half-open recovery probe
- Structured JSON logs with validated `X-Request-ID` correlation
- Prometheus-compatible counters and a liveness endpoint
- Tests using `httptest`, plus race detection and `go vet` in CI

## Quick start

Start any HTTP server on port `9000`, for example:

```sh
python3 -m http.server 9000
```

In another terminal:

```sh
go run ./cmd/proxy -backend http://localhost:9000 -listen :8080
curl -i http://localhost:8080/
curl -i http://localhost:8080/healthz
curl -s http://localhost:8080/metrics
```

Build a binary with:

```sh
go build -trimpath -o bin/reverse-proxy ./cmd/proxy
```

## Configuration

| Flag | Default | Description |
| --- | --- | --- |
| `-listen` | `:8080` | Address on which the proxy listens |
| `-backend` | `http://localhost:9000` | Fixed upstream URL; only `http` and `https` are accepted |
| `-rate-limit` | `0` | Requests per second; `0` disables the process-local limiter |
| `-upstream-timeout` | `15s` | Deadline for each upstream request |

The configured backend must not contain credentials or a URL fragment. Keep backend configuration private; this binary is not an open proxy or an authentication layer.

## Request flow

```text
client
  │
  ▼
HTTP server → request ID/logging → local endpoints
                         │
                         └→ rate limit → circuit breaker → timeout → upstream
                                                                    │
                                                                    ▼
                                                               backend
```

`/healthz` and `/metrics` are served locally and accept `GET` and `HEAD`. Other paths preserve the incoming method, path, query string, headers, and body as `httputil.ReverseProxy` permits. Hop-by-hop headers are handled by the standard library.

The circuit breaker counts forwarded `5xx` responses and proxy errors. After five failures it opens for ten seconds, then admits one probe; a successful probe closes it. The rate limiter and breaker are deliberately process-local, so they do not provide a distributed quota or circuit across multiple instances.

## Operational behavior

- Upstream requests have a 15-second default deadline and a custom transport with a 100-connection-per-host cap.
- The server limits request header size, times out header reads, and closes idle upstream connections during shutdown.
- Every request receives or propagates a safe `X-Request-ID`; logs are JSON on stdout.
- `GET /healthz` is a liveness check and does not verify backend health.
- `GET /metrics` exposes counters such as requests, `5xx` responses, in-flight upstream requests, rate-limit rejections, circuit-open rejections, and upstream failures.

## Development

```sh
make test       # race-enabled tests with coverage
make vet        # go vet
make build      # compile the binary
```

Equivalent commands without `make`:

```sh
go test -race -cover ./...
go vet ./...
go build ./cmd/proxy
```

The repository's CI runs formatting checks, tests with the race detector, coverage, vetting, and a build.

## Deliberate boundaries

This is a focused gateway, not a complete internet-facing edge platform. It does not include TLS termination, authentication, dynamic routing, retries, distributed rate limiting, request-body limits, or a full histogram-based latency system. In a real deployment, put it behind an authenticated/TLS-terminating edge and add those controls according to the threat model.

Those omissions are part of the design discussion: the implementation keeps the standard-library failure modes visible rather than hiding them behind a framework.

## Repository map

- `cmd/proxy/main.go` — flags, server lifecycle, signal handling, and shutdown
- `internal/proxy/proxy.go` — configuration, routing, forwarding, middleware, and response tracking
- `internal/proxy/circuit_breaker.go` — closed/open/half-open failure state
- `internal/proxy/rate_limiter.go` — process-local fixed-window limiter
- `internal/proxy/metrics.go` — Prometheus text exposition
- `internal/proxy/proxy_test.go` — forwarding, resilience, timeout, and configuration tests
- `lessons/` and `reference/` — first-principles learning material

## Demo and portfolio publishing

The Quick start section is a reproducible local demo. For a portfolio walkthrough, record a short screen capture showing:

1. the backend and proxy starting;
2. a request being forwarded with its path and query string;
3. `/healthz` and `/metrics`; and
4. one resilience behavior, such as a timeout or the circuit opening after repeated `5xx` responses.

If you deploy a public instance, add its URL here alongside the recording. Do not expose an unrestricted proxy; keep the upstream fixed and place authentication/TLS in front of it.

This is already a credible focused portfolio project because it has a clear problem, a small implementation, operational safeguards, tests for failure paths, and documented tradeoffs. Avoid adding unrelated features just to increase line count.
