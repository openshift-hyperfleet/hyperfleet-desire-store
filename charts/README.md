# hyperfleet-desire-store

![Version: 1.0.0](https://img.shields.io/badge/Version-1.0.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.0.0-dev](https://img.shields.io/badge/AppVersion-0.0.0--dev-informational?style=flat-square)

Desire Store API service that transports desires from HyperFleet adapters to appliers

**Homepage:** <https://github.com/openshift-hyperfleet/hyperfleet-desire-store>
> For the service settings, see [docs/config.md](https://github.com/openshift-hyperfleet/hyperfleet-desire-store/blob/main/docs/config.md). For the metrics and alerts, see [docs/metrics.md](https://github.com/openshift-hyperfleet/hyperfleet-desire-store/blob/main/docs/metrics.md) and [docs/alerts.md](https://github.com/openshift-hyperfleet/hyperfleet-desire-store/blob/main/docs/alerts.md).

## Installation

The image registry, repository and tag, and the six required service settings,
have no default. The render fails while one is empty.

```bash
helm install hyperfleet-desire-store ./charts \
  --set image.registry=quay.io \
  --set image.repository=openshift-hyperfleet/hyperfleet-desire-store \
  --set image.tag=<version> \
  --set config.partitionClaim=<claim> \
  --set config.dbMaxConcurrency=<n> \
  --set config.sweeperIdentity=system:serviceaccount:<namespace>:<name> \
  --set config.wristband.issuer=<issuer> \
  --set config.wristband.audience=<audience> \
  --set config.wristband.jwksUrl=<url>
```

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| HyperFleet Team |  |  |

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| replicaCount | int | `1` | Number of replicas. Set to 0 to stop all replicas while keeping the release. |
| image.registry | string | `"CHANGE_ME"` | Container image registry (no default — must be set) |
| image.repository | string | `"CHANGE_ME"` | Container image repository (no default — must be set) |
| image.pullPolicy | string | `"Always"` | Image pull policy |
| image.tag | string | `""` | Image tag (no default — must be set via `--set image.tag=<version>`) |
| imagePullSecrets | list | `[]` | Secrets for pulling images from private registries |
| nameOverride | string | `""` | Override the chart name used in resource names |
| fullnameOverride | string | `""` | Override the full release name used in resource names |
| serviceAccount | object | `{"annotations":{},"create":true,"name":""}` | ServiceAccount configuration |
| serviceAccount.create | bool | `true` | Create a ServiceAccount |
| serviceAccount.annotations | object | `{}` | Annotations added to the ServiceAccount |
| serviceAccount.name | string | `""` | Override the ServiceAccount name (defaults to the release fullname) |
| podAnnotations | object | `{}` | Additional annotations applied to all pods |
| podLabels | object | `{}` | Additional labels applied to all pods |
| podSecurityContext | object | `{"fsGroup":65532,"runAsNonRoot":true,"runAsUser":65532}` | Pod-level security context |
| podSecurityContext.fsGroup | int | `65532` | Filesystem group for volume mounts |
| podSecurityContext.runAsNonRoot | bool | `true` | Run all containers as non-root |
| podSecurityContext.runAsUser | int | `65532` | UID for all containers |
| securityContext | object | `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"readOnlyRootFilesystem":true,"seccompProfile":{"type":"RuntimeDefault"}}` | Container-level security context |
| securityContext.readOnlyRootFilesystem | bool | `true` | Mount root filesystem as read-only |
| securityContext.allowPrivilegeEscalation | bool | `false` | Disallow privilege escalation |
| resources | object | `{"limits":{"cpu":"500m","memory":"512Mi"},"requests":{"cpu":"100m","memory":"128Mi"}}` | CPU and memory resource requests and limits |
| nodeSelector | object | `{}` | Node selector constraints for pod scheduling |
| tolerations | list | `[]` | Tolerations for pod scheduling |
| affinity | object | `{}` | Affinity rules for pod scheduling |
| podDisruptionBudget | object | `{"enabled":false,"maxUnavailable":1}` | PodDisruptionBudget configuration |
| podDisruptionBudget.enabled | bool | `false` | Enable the PDB |
| podDisruptionBudget.maxUnavailable | int | `1` | Maximum number of pods that can be unavailable during disruption |
| ports | object | `{"api":8000}` | Listen port of the data plane (`HYPERFLEET_SERVER_ADDR` is generated from it) |
| ports.api | int | `8000` | Data-plane port |
| observability | object | `{"healthPort":8080,"metricsPort":9090,"probes":{"liveness":{"failureThreshold":3,"initialDelaySeconds":15,"path":"/healthz","periodSeconds":20,"timeoutSeconds":5},"readiness":{"failureThreshold":3,"initialDelaySeconds":5,"path":"/readyz","periodSeconds":10,"timeoutSeconds":3}}}` | Health and metrics listeners. The listen addresses the service reads are generated from these ports, so the probes, the Service and the ServiceMonitor always match the listeners. |
| observability.healthPort | int | `8080` | Port of `/healthz` and `/readyz` |
| observability.metricsPort | int | `9090` | Port of `/metrics` |
| observability.probes.liveness.path | string | `"/healthz"` | Liveness probe path |
| observability.probes.liveness.initialDelaySeconds | int | `15` | Seconds before the first liveness probe |
| observability.probes.liveness.periodSeconds | int | `20` | Seconds between liveness probes |
| observability.probes.liveness.timeoutSeconds | int | `5` | Liveness probe timeout in seconds |
| observability.probes.liveness.failureThreshold | int | `3` | Failed liveness probes before the container restarts |
| observability.probes.readiness.path | string | `"/readyz"` | Readiness probe path |
| observability.probes.readiness.initialDelaySeconds | int | `5` | Seconds before the first readiness probe |
| observability.probes.readiness.periodSeconds | int | `10` | Seconds between readiness probes |
| observability.probes.readiness.timeoutSeconds | int | `3` | Readiness probe timeout in seconds |
| observability.probes.readiness.failureThreshold | int | `3` | Failed readiness probes before the pod leaves the Service |
| terminationGracePeriodSeconds | int | `45` | Seconds Kubernetes waits for the pod to stop. Must exceed the `preStop` sleep plus `config.shutdownTimeoutSeconds`. The render fails when it does not. |
| lifecycle | object | `{"preStop":{"exec":{"command":["sleep","5"]}}}` | Container lifecycle hooks. The `preStop` sleep lets endpoint removal propagate before the service starts to shut down. The render accepts `preStop.sleep.seconds`, `exec` `["sleep", "N"]`, or a shell running `sleep N`. Set `lifecycle.preStop.exec` to `null` when using `preStop.sleep.seconds`. |
| config | object | See values.yaml | Service settings, rendered to a ConfigMap of `HYPERFLEET_*` environment variables (see docs/config.md). The listen addresses are not settings here: they come from `ports.api`, `observability.healthPort` and `observability.metricsPort`. Settings marked "required" have no default and the render fails while they are empty. |
| config.partitionClaim | string | `""` | Wristband claim that carries the caller's partition (required) |
| config.dbMaxConcurrency | string | `""` | Connection pool size of the Postgres store, at least 1 (required) |
| config.sweeperIdentity | string | `""` | `hf_identity` of the centralized sweeper, `system:serviceaccount:<namespace>:<name>` (required) |
| config.wristband.issuer | string | `""` | `iss` claim every Wristband must carry (required) |
| config.wristband.audience | string | `""` | `aud` claim every Wristband must carry (required) |
| config.wristband.jwksUrl | string | `""` | Absolute `http` or `https` URL of the key set that signs Wristbands (required) |
| config.wristband.jwksCaFile | string | `""` | Path of a PEM CA bundle trusted in addition to the system trust store. Empty uses the service default. |
| config.wristband.algorithms | string | `""` | Accepted signing algorithms, separated by commas. Empty uses the service default (`ES256`). |
| config.shutdownTimeoutSeconds | int | `20` | Graceful shutdown budget in whole seconds (1 to 9223372036). |
| config.log.level | string | `""` | Log level (`debug`, `info`, `warn`, `error`). Empty uses the service default. |
| config.log.format | string | `""` | Log format (`json` or `text`). Empty uses the service default. |
| config.log.output | string | `""` | Log output (`stdout` or `stderr`). Empty uses the service default. |
| extraEnv | list | `[]` | Additional environment variables for the container, for example database credentials from a Secret |
| extraEnvFrom | list | `[]` | Additional `envFrom` sources for the container |
| extraVolumes | list | `[]` | Additional volumes for the pod, for example a ConfigMap or Secret that holds the CA bundle `config.wristband.jwksCaFile` points to |
| extraVolumeMounts | list | `[]` | Additional volume mounts for the container |
| monitoring | object | `{"dashboard":{"enabled":false},"prometheusRule":{"additionalLabels":{},"alerts":{"down":{"for":"5m"},"highErrorRatio":{"for":"10m","minRequestRate":0.1,"threshold":0.05},"highLatency":{"for":"10m","minRequestRate":0.1,"thresholdSeconds":1}},"enabled":false,"namespace":""},"serviceMonitor":{"enabled":false,"honorLabels":true,"interval":"30s","labels":{},"metricRelabeling":[],"namespace":"","namespaceSelector":{},"scrapeTimeout":"10s"}}` | Monitoring integration. Each resource needs the Prometheus Operator CRDs or a Grafana dashboard sidecar from the monitoring stack. |
| monitoring.serviceMonitor | object | `{"enabled":false,"honorLabels":true,"interval":"30s","labels":{},"metricRelabeling":[],"namespace":"","namespaceSelector":{},"scrapeTimeout":"10s"}` | ServiceMonitor for Prometheus Operator scrape configuration |
| monitoring.serviceMonitor.enabled | bool | `false` | Create a ServiceMonitor for the metrics port |
| monitoring.serviceMonitor.namespace | string | `""` | Namespace of the ServiceMonitor (defaults to the release namespace) |
| monitoring.serviceMonitor.labels | object | `{}` | Additional labels on the ServiceMonitor |
| monitoring.serviceMonitor.interval | string | `"30s"` | Scrape interval |
| monitoring.serviceMonitor.scrapeTimeout | string | `"10s"` | Scrape timeout |
| monitoring.serviceMonitor.honorLabels | bool | `true` | Honor labels from the target to avoid overwriting |
| monitoring.serviceMonitor.metricRelabeling | list | `[]` | Metric relabeling rules |
| monitoring.serviceMonitor.namespaceSelector | object | `{}` | Namespace selector for cross-namespace monitoring |
| monitoring.prometheusRule | object | `{"additionalLabels":{},"alerts":{"down":{"for":"5m"},"highErrorRatio":{"for":"10m","minRequestRate":0.1,"threshold":0.05},"highLatency":{"for":"10m","minRequestRate":0.1,"thresholdSeconds":1}},"enabled":false,"namespace":""}` | PrometheusRule for alerting |
| monitoring.prometheusRule.enabled | bool | `false` | Create a PrometheusRule with the alerts. `HyperFleetDesireStoreDown` renders only when `monitoring.serviceMonitor.enabled` is also true. |
| monitoring.prometheusRule.namespace | string | `""` | Namespace of the PrometheusRule (defaults to the release namespace) |
| monitoring.prometheusRule.additionalLabels | object | `{}` | Additional labels on the PrometheusRule |
| monitoring.prometheusRule.alerts | object | See values.yaml | Alert thresholds. The numbers are provisional: no production traffic exists to derive them from. |
| monitoring.prometheusRule.alerts.down.for | string | `"5m"` | How long the target must be down or absent before the alert fires |
| monitoring.prometheusRule.alerts.highErrorRatio.threshold | float | `0.05` | 5xx share of requests above which the alert fires (0.05 is 5%) |
| monitoring.prometheusRule.alerts.highErrorRatio.minRequestRate | float | `0.1` | Requests per second below which the alert cannot fire |
| monitoring.prometheusRule.alerts.highErrorRatio.for | string | `"10m"` | How long the ratio must stay above the threshold |
| monitoring.prometheusRule.alerts.highLatency.thresholdSeconds | int | `1` | p99 request duration in seconds above which the alert fires. Use a histogram bucket edge to avoid interpolation. |
| monitoring.prometheusRule.alerts.highLatency.minRequestRate | float | `0.1` | Requests per second below which the alert cannot fire |
| monitoring.prometheusRule.alerts.highLatency.for | string | `"10m"` | How long the latency must stay above the threshold |
| monitoring.dashboard | object | `{"enabled":false}` | Grafana dashboard provisioning via sidecar ConfigMap |
| monitoring.dashboard.enabled | bool | `false` | Create a ConfigMap with the Grafana dashboard (label `grafana_dashboard: "1"`) |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs](https://github.com/norwoodj/helm-docs)
