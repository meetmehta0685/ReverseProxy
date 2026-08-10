# Go Reverse Proxy Resources

## Knowledge

- [Go tutorial: Get started with Go](https://go.dev/doc/tutorial/getting-started)
  Official zero-assumption introduction to packages, imports, `func main`, modules, and the `go` command. Use for: the first lessons and basic tool vocabulary.
- [A Tour of Go](https://go.dev/tour/list)
  Official interactive sequence from basic syntax through methods, interfaces, and concurrency. Use for: short practice alongside the project's concepts.
- [Standard: RFC 9110 — HTTP Semantics](https://www.rfc-editor.org/rfc/rfc9110.html)
  The authoritative vocabulary and behavioral rules for HTTP messages, methods, status codes, fields, and intermediaries. Use for: every claim about HTTP and what a reverse proxy must preserve.
- [Go package: `net/http`](https://pkg.go.dev/net/http)
  The contract for handlers, requests, response writers, servers, transports, timeouts, and graceful shutdown. Use for: the server and HTTP client halves of the project.
- [Go package: `net/http/httputil`](https://pkg.go.dev/net/http/httputil)
  The standard-library `ReverseProxy` implementation and its URL/header rewriting rules. Use for: the forwarding core and its security-sensitive defaults.
- [Go package: `context`](https://pkg.go.dev/context)
  Defines propagation of cancellation, deadlines, and request-scoped values. Use for: upstream timeouts, shutdown, and request IDs passed through a call chain.
- [Go package: `net/http/httptest`](https://pkg.go.dev/net/http/httptest)
  Test servers, requests, and response recorders for HTTP code. Use for: understanding and extending this project's tests.
- [Go article: Data Race Detector](https://go.dev/doc/articles/race_detector)
  Official definition, usage, and limits of runtime race detection. Use for: validating the mutexes and atomics in shared request state.
- [Go blog: Structured Logging with `slog`](https://go.dev/blog/slog)
  Rationale and standard-library patterns for searchable key-value logs. Use for: request logging and operational diagnosis.

## Wisdom (Communities)

- [Go Forum](https://forum.golangbridge.org/)
  Long-running, moderated community for design feedback and debugging questions. Use for: having an implementation or API choice critiqued by practicing Go developers.
- [Go issue tracker](https://github.com/golang/go/issues)
  Maintainer discussions reveal edge cases and design intent behind `net/http`. Use for: investigating surprising standard-library behavior after consulting package docs.

## Gaps

- Add primary operational references when the course reaches container deployment, production metrics, and edge-security hardening; those targets are not yet specified by this repository.
