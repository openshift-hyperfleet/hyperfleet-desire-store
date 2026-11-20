# Alerts

The Helm chart renders a `PrometheusRule` when `monitoring.prometheusRule.enabled`
is `true`. It holds three alerts. Each query is scoped to the release's
namespace and Service, so one Prometheus can scrape several releases without
mixing them. Every threshold, minimum request rate and wait time (`for`) is a
value under `monitoring.prometheusRule.alerts`. The defaults are in
[charts/README.md](../charts/README.md).

The thresholds are provisional. No production traffic exists to derive them
from. Tune them with the capacity work in
[HYPERFLEET-1842](https://redhat.atlassian.net/browse/HYPERFLEET-1842).

The request metrics come from the REST router, which `serve` does not mount
yet (see [metrics.md](metrics.md)). Until it does, `HyperFleetDesireStoreHighErrorRatio`
and `HyperFleetDesireStoreHighLatency` have no data and cannot fire.
`HyperFleetDesireStoreDown` works now.

## HyperFleetDesireStoreDown

**Severity:** critical. **Renders only when** `monitoring.serviceMonitor.enabled`
is `true`. Without the ServiceMonitor, `up` does not exist and the alert would
fire permanently.

**Expression:** `absent(up{...}) or up{...} == 0` for this release's Service.

**Wait time:** `monitoring.prometheusRule.alerts.down.for`.

**Impact:** Prometheus cannot scrape the metrics port. The service may be down,
not ready, or unreachable from Prometheus. Requests may be failing.

**Response:**

1. List the pods with `kubectl get pods --namespace <namespace> -l app.kubernetes.io/instance=<release>`.
2. Read the logs of a failing pod. A missing or invalid required setting fails startup (see [config.md](config.md)).
3. Check that the Service has endpoints and that the ServiceMonitor selects it.

## HyperFleetDesireStoreHighErrorRatio

**Severity:** warning.

**Expression:** the share of requests with a `5xx` `status_code` over 5 minutes
is above the threshold, and the request rate is above a minimum.

The minimum rate keeps a handful of failed requests from firing the alert.

**Impact:** callers get server errors. Owner and version conflicts
(`HYPERFLEET-CNF-004`, `HYPERFLEET-CNF-002`) are 4xx answers and do not count.

**Response:**

1. Open the dashboard panels "5xx ratio by path" and "Errors by error code" to see which route and `error_code` fail.
2. Read the logs for "request failed" records, which carry `path` and `trace_id`.
3. Check the database the service depends on.

## HyperFleetDesireStoreHighLatency

**Severity:** warning.

**Expression:** the p99 of `hyperfleet_desire_store_request_duration_seconds`
over 5 minutes is above the threshold, and the request rate is above a minimum.
The p99 is computed over all routes of the release together.

Use a histogram bucket edge for the threshold (`0.005`, `0.01`, `0.025`,
`0.05`, `0.1`, `0.25`, `0.5`, `1`, `2.5`, `5`, `10`), so the quantile is not
interpolated inside a bucket.

**Impact:** callers wait longer than expected. Adapters and appliers may time
out.

**Response:**

1. Open the dashboard panel "Request latency by path" to find the slow route.
2. Compare with the request rate panel: a rise in load explains a rise in latency.
3. Check the database and the pod's CPU and memory panels.
