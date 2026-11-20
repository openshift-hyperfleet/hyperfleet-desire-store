# Metrics

The Desire Store API exposes Prometheus metrics on `GET /metrics` of the
metrics listener. The listener address is set by `--metrics-addr` or
`HYPERFLEET_METRICS_ADDR` and defaults to `:9090`, the port and path that the
[metrics standard](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/metrics.md)
fixes. The listener has no authentication. Expose it only to the monitoring
stack, not through the gateway.

The request metrics count what `handlers.NewRouter` serves. `serve` does not
mount that router yet, so a running process shows only `hyperfleet_desire_store_build_info`
and the Go and process metrics until the store is wired in. An unobserved
metric with labels emits no lines.

## Metrics

Every metric has the constant labels `component` (`desire-store`) and
`version`.

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `hyperfleet_desire_store_requests_total` | counter | `method`, `path`, `status_code` | Requests served, rejected ones included. |
| `hyperfleet_desire_store_request_duration_seconds` | histogram | `method`, `path`, `status_code` | Request duration in seconds. |
| `hyperfleet_desire_store_errors_total` | counter | `method`, `path`, `error_code` | Requests answered with an Error Model problem, by `error_code`. |
| `hyperfleet_desire_store_build_info` | gauge | `commit`, `go_version` | Always 1. |

The Go collector (`go_*`) and the process collector (`process_*`) are
registered as well.

Buckets of the duration histogram, in seconds: `0.005`, `0.01`, `0.025`,
`0.05`, `0.1`, `0.25`, `0.5`, `1`, `2.5`, `5`, `10`.

### Label values

| Label | Values |
| --- | --- |
| `method` | The request method, or `OTHER` for a method that is not a standard HTTP method. The method is read before authentication, so a caller cannot create series with it. |
| `path` | The route pattern without the method: `/api/hyperfleet/desire-store/v1/partitions/{partition}/desires`, the same with `/{id}`, and the same with `/{id}/status`. `unmatched` for a request that matches no route (404) or matches a route with another method (405). |
| `status_code` | The HTTP status that the client received. |
| `error_code` | The Error Model code of the problem, for example `HYPERFLEET-CNF-004` for an owner conflict and `HYPERFLEET-CNF-002` for a version conflict. The set is fixed in the code. |

A 401 is counted with the route's `path` and an `HYPERFLEET-AUT-*` code, because
the instrumentation wraps authentication. A 405 keeps the standard library
response, which has no Error Model problem, so it has no `errors_total` sample.

### Queries

Error ratio by route:

```promql
(
  sum by (path) (rate(hyperfleet_desire_store_requests_total{status_code=~"5.."}[5m]))
    or 0 * sum by (path) (rate(hyperfleet_desire_store_requests_total[5m]))
)
  / sum by (path) (rate(hyperfleet_desire_store_requests_total[5m]))
```

p99 latency by route:

```promql
histogram_quantile(0.99,
  sum by (path, le) (rate(hyperfleet_desire_store_request_duration_seconds_bucket[5m])))
```

## Scraping

The Helm chart in `charts/` exposes the metrics listener as the Service port
`metrics` (`observability.metricsPort`, default `9090`). Three toggles under
`monitoring` turn on the monitoring resources. All three default to `false`.

| Value | Resource | Needs |
| --- | --- | --- |
| `monitoring.serviceMonitor.enabled` | `ServiceMonitor` that scrapes `/metrics` of the `metrics` port | Prometheus Operator CRDs |
| `monitoring.prometheusRule.enabled` | `PrometheusRule` with the alerts in [alerts.md](alerts.md) | Prometheus Operator CRDs |
| `monitoring.dashboard.enabled` | `ConfigMap` with the Grafana dashboard, label `grafana_dashboard: "1"` | A Grafana dashboard sidecar that reads that label |

The Prometheus Operator and the Grafana sidecar belong to the monitoring stack,
not to this chart. The `kube-prometheus-stack` values in `hyperfleet-infra`
(`helmfile/values/base-kube-prometheus-stack.yaml.gotmpl`) set
`serviceMonitorSelectorNilUsesHelmValues` and `ruleSelectorNilUsesHelmValues`
to `false`, so that stack selects the resources without a release label. For
any other stack, check its selectors `[unverified]`.

The `PrometheusRule` queries are scoped to the release's namespace and Service
through the labels the ServiceMonitor adds to each target; see
[alerts.md](alerts.md).

The metrics listener has no authentication, so keep the Service inside the
cluster.

## Dashboard

`charts/dashboards/hyperfleet-desire-store.json` has the template variables
`datasource`, `namespace` and `service`. Every panel filters on `namespace` and
`service`.

No panel uses a `partition` label (see [Per-partition data](#per-partition-data)).
The request panels stay empty until `serve` mounts the router.

## Per-partition data

**Decision:** no metric carries a `partition` label. Per-partition questions
are answered from logs and traces.

**Rationale:** the metrics standard asks for fewer than 100 unique values per
label and says not to use high-cardinality labels. The planning workload
(from the ticket notes, not from this repository) is 10,000 partitions. One label set of `method`, `path` and `status_code` makes 15
series: 1 for the counter, and 11 buckets, `+Inf`, `_sum` and `_count` for the
histogram. The Applier's steady state uses 2 label sets per partition (a list
that returns 200 and a status write that returns 204). That is 15 × 2 × 10,000
= 300,000 series per replica, before Adapter traffic and error codes. Without
the label, the series count depends on routes and status codes, not on the
number of partitions.

**Where per-partition data is today:**

- Rejected and failed requests: the log record "request rejected" or "request
  failed" carries `path`, which contains the partition, and `trace_id`
  (`internal/handlers/helpers.go`).

**Gaps:**

- Logs: no record is written for a successful request. The logging
  specification describes a completion record with `method`, `path`,
  `status_code` and `duration_ms` for HTTP services. This service does not
  write it yet.
- Traces: this service creates no spans yet, so a trace cannot answer a
  per-partition question for a successful request. The tracing standard
  sanitizes `url.path` and allows high-cardinality values as span attributes,
  so an attribute such as `hyperfleet.partition` would be the place for it.

**Revisit when:** the number of partitions planned for one deployment is below
100.
