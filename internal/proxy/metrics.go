package proxy

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
)

type metrics struct {
	requests         atomic.Uint64
	errors           atomic.Uint64
	inFlight         atomic.Int64
	rateLimited      atomic.Uint64
	circuitOpen      atomic.Uint64
	upstreamFailures atomic.Uint64
}

func (m *metrics) write(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	var output strings.Builder
	writeCounter(&output, "proxy_requests_total", "Total HTTP requests handled by the proxy.", m.requests.Load())
	writeCounter(&output, "proxy_errors_total", "Total responses with a 5xx status.", m.errors.Load())
	writeGauge(&output, "proxy_in_flight", "Requests currently being forwarded upstream.", m.inFlight.Load())
	writeCounter(&output, "proxy_rate_limited_total", "Requests rejected by the rate limiter.", m.rateLimited.Load())
	writeCounter(&output, "proxy_circuit_open_total", "Requests rejected because the upstream circuit was open.", m.circuitOpen.Load())
	writeCounter(&output, "proxy_upstream_failures_total", "Upstream attempts that produced a 5xx response or proxy error.", m.upstreamFailures.Load())
	_, _ = io.WriteString(w, output.String())
}

func writeCounter(output *strings.Builder, name, help string, value uint64) {
	output.WriteString("# HELP ")
	output.WriteString(name)
	output.WriteByte(' ')
	output.WriteString(help)
	output.WriteByte('\n')
	output.WriteString("# TYPE ")
	output.WriteString(name)
	output.WriteString(" counter\n")
	output.WriteString(name)
	output.WriteByte(' ')
	output.WriteString(strconv.FormatUint(value, 10))
	output.WriteByte('\n')
}

func writeGauge(output *strings.Builder, name, help string, value int64) {
	output.WriteString("# HELP ")
	output.WriteString(name)
	output.WriteByte(' ')
	output.WriteString(help)
	output.WriteByte('\n')
	output.WriteString("# TYPE ")
	output.WriteString(name)
	output.WriteString(" gauge\n")
	output.WriteString(name)
	output.WriteByte(' ')
	output.WriteString(strconv.FormatInt(value, 10))
	output.WriteByte('\n')
}
