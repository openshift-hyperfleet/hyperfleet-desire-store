// Package metrics owns the Prometheus registry of the Desire Store API and
// the collectors in it, named per the HyperFleet metrics standard.
//
// The metrics are:
//
//   - hyperfleet_desire_store_requests_total (method, path, status_code)
//   - hyperfleet_desire_store_request_duration_seconds (method, path, status_code)
//   - hyperfleet_desire_store_errors_total (method, path, error_code)
//   - hyperfleet_desire_store_build_info (commit, go_version)
//
// All carry the constant labels component and version. The Go and process
// collectors are registered too. No metric has a partition label: with 10,000
// partitions it would exceed the standard's limit of 100 values per label.
// See docs/metrics.md.
package metrics

import (
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	namespace = "hyperfleet_desire_store"
	component = "desire-store"

	labelMethod = "method"
	labelPath   = "path"
)

var requestLabels = []string{labelMethod, labelPath, "status_code"}

// durationBuckets are the standard's recommendation for HTTP request duration.
var durationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Metrics holds the registry and the request collectors.
type Metrics struct {
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	errors   *prometheus.CounterVec
}

// New builds a private registry, so that every instance, in tests too, counts
// on its own.
func New(version, commit string) *Metrics {
	constLabels := prometheus.Labels{"component": component, "version": version}
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace:   namespace,
			Name:        "requests_total",
			Help:        "Total number of HTTP requests served by the data plane.",
			ConstLabels: constLabels,
		}, requestLabels),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace:   namespace,
			Name:        "request_duration_seconds",
			Help:        "Duration of HTTP requests served by the data plane, in seconds.",
			ConstLabels: constLabels,
			Buckets:     durationBuckets,
		}, requestLabels),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace:   namespace,
			Name:        "errors_total",
			Help:        "Total number of requests answered with an Error Model problem, by error code.",
			ConstLabels: constLabels,
		}, []string{labelMethod, labelPath, "error_code"}),
	}
	buildInfo := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "build_info",
		Help:      "Build information of the running service. The value is always 1.",
		ConstLabels: prometheus.Labels{
			"component": component, "version": version, "commit": commit, "go_version": runtime.Version(),
		},
	})
	buildInfo.Set(1)

	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.requests, m.duration, m.errors, buildInfo,
	)
	return m
}

// Handler serves the registry in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// ObserveRequest records one served request. A non-empty errorCode also
// counts an error.
func (m *Metrics) ObserveRequest(method, path string, status int, errorCode string, elapsed time.Duration) {
	code := strconv.Itoa(status)
	m.requests.WithLabelValues(method, path, code).Inc()
	m.duration.WithLabelValues(method, path, code).Observe(elapsed.Seconds())
	if errorCode != "" {
		m.errors.WithLabelValues(method, path, errorCode).Inc()
	}
}
