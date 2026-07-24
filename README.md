# Reverse Proxy

A small, production-minded HTTP reverse proxy written in Go's standard library. It is designed as a clear portfolio project: one binary, no external services, predictable operational behavior, and tests around the critical path.

## Features

- Forwards HTTP methods, paths, query strings, headers, and response bodies to one upstream.
- Graceful shutdown on `SIGINT`/`SIGTERM`.
- Bounded upstream connections and request timeouts.
- In-process rate limiting (`-rate-limit`) and a circuit breaker for repeated upstream failures.
- Structured JSON request logs.
- `GET /healthz` liveness endpoint and `GET /metrics` Prometheus-style counters.
- Race-tested CI with `go vet`.

## Run it

```sh
go run ./cmd/proxy -backend http://localhost:9000 -listen :8080
```

Useful flags:

```text
-listen             address to bind (default :8080)
-backend            upstream URL (default http://localhost:9000)
-rate-limit         requests per second; 0 disables it
-upstream-timeout   per-request upstream deadline (default 15s)
```

Then send traffic to `http://localhost:8080`. The proxy keeps the original request path and query string.

## Design notes

The implementation deliberately uses `net/http` and `httputil.ReverseProxy` instead of a framework. This keeps the failure modes visible and the binary easy to audit. The circuit breaker opens after five upstream failures and allows requests again after a ten-second cool-down; rate limiting is process-local and intended as a lightweight edge safeguard, not a distributed quota system.

## Development

```sh
go test -race -cover ./...
go vet ./...
```

The repository is intended to be public and contains no deployment configuration or secrets.
