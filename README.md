# HyperFleet Desire Store API

The Desire Store API transports desires from HyperFleet adapters to appliers.
It is a rebuildable delivery channel, not a source of truth: if the store is
lost, an adapter recomputes and rewrites its desires from API state.

The service is part of the Desire Store design:

- [Desire Store Detailed Design](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/docs/desire-store-dd.md) — REST v1 contract, authorization, and failure behavior.
- [Desire Store HLD](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/docs/desire-store-hld.md) — system context and topology.

## Status

The process starts, serves the standard health and metrics endpoints, shuts down
gracefully, builds a FIPS-compliant image, and ships a Helm chart with
monitoring resources (see [Helm chart](#helm-chart)).

The REST v1 service is implemented in the same layers as hyperfleet-api:
`internal/handlers` decodes requests and writes responses, `internal/services`
holds the business rules, `internal/presenters` fills the wire shapes, and
`internal/errors` defines the one error type. The request and response types
are generated from the
[OpenAPI contract](https://github.com/openshift-hyperfleet/hyperfleet-desire-store-spec)
with `oapi-codegen` into `pkg/api/openapi`, as ADR-0003 requires; the
generated code is not committed. Every request is checked against the
contract by oapi-codegen's `nethttp-middleware` request validator before a
handler runs; the handlers keep only the rules the contract states in prose. It validates Wristbands,
authorizes callers from the Wristband claims, serves the five operations, and
answers errors in the Error Model format. Its tests run
against the shared store interface with the Applier's in-memory store.

`serve` does not mount this layer yet. Every data-plane path returns a
plain-text 404. The Postgres store backend
([HYPERFLEET-1671](https://redhat.atlassian.net/browse/HYPERFLEET-1671)) mounts
it.

## Quick start

```bash
make generate   # OpenAPI types and client into pkg/api/openapi; build, test, vet, and lint run it
make build      # bin/hyperfleet-desire-store
make run        # serve with text logs on :8000, :8080 and :9090; set the required settings first
make test       # unit tests with the race detector
make lint       # golangci-lint, pinned in tools/go.mod
make ci-validate  # everything CI runs, except the image build
```

`make help` lists every target.

### Endpoints

| Port | Endpoint | Purpose |
| --- | --- | --- |
| `8080` | `/healthz` | Liveness. Reports process state only, never a dependency. |
| `8080` | `/readyz` | Readiness. `503` during startup and across the shutdown drain. |
| `9090` | `/metrics` | Prometheus metrics. See [docs/metrics.md](docs/metrics.md). |
| `8000` | `/api/hyperfleet/desire-store/v1/...` | Desire Store REST v1 data plane, not mounted yet (see [Status](#status)). |

The probes follow the [health endpoints standard](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/health-endpoints.md).
`/metrics` follows the [metrics standard](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/metrics.md).

### Configuration

Flags win over environment variables, and environment variables win over the
defaults. A malformed value fails startup instead of being ignored.

| Flag | Environment variable | Default |
| --- | --- | --- |
| `--server-addr` | `HYPERFLEET_SERVER_ADDR` | `:8000` |
| `--health-addr` | `HYPERFLEET_HEALTH_ADDR` | `:8080` |
| `--metrics-addr` | `HYPERFLEET_METRICS_ADDR` | `:9090` |
| `--shutdown-timeout` | `SHUTDOWN_TIMEOUT` | `20s` |
| `--log-level`, `-l` | `HYPERFLEET_LOG_LEVEL` | `info` |
| `--log-format`, `-f` | `HYPERFLEET_LOG_FORMAT` | `json` |
| `--log-output` | `HYPERFLEET_LOG_OUTPUT` | `stdout` |
| `--partition-claim` | `HYPERFLEET_PARTITION_CLAIM` | none, required |
| `--wristband-issuer` | `HYPERFLEET_WRISTBAND_ISSUER` | none, required |
| `--wristband-audience` | `HYPERFLEET_WRISTBAND_AUDIENCE` | none, required |
| `--wristband-jwks-url` | `HYPERFLEET_WRISTBAND_JWKS_URL` | none, required |
| `--wristband-jwks-ca-file` | `HYPERFLEET_WRISTBAND_JWKS_CA_FILE` | none: the system trust store |
| `--wristband-algorithms` | `HYPERFLEET_WRISTBAND_ALGORITHMS` | `ES256` |
| `--sweeper-identity` | `HYPERFLEET_SWEEPER_IDENTITY` | none, required |
| `--db-max-concurrency` | `HYPERFLEET_DB_MAX_CONCURRENCY` | none, required |

`serve`, and so `make run`, fails at startup when a required setting is
missing or invalid. The Wristband settings name the issuer, audience, signing
algorithms, and key set of the Wristbands that the gateway issues.
`--partition-claim` names the Wristband claim that carries the caller's
partition. No setting turns the partition check off. `--sweeper-identity` is
the `hf_identity` of the centralized sweeper. `--db-max-concurrency` is the
connection pool size for the Postgres store.

`serve` validates these settings, but nothing uses them until the REST v1 layer
is mounted (see [Status](#status)).

See [Configuration](docs/config.md) for the complete option reference,
validation rules, and supported configuration sources.

Settings and naming follow the [configuration standard](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/configuration.md)
and the [logging specification](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/logging-specification.md).

On `SIGTERM` or `SIGINT` the service marks itself not ready, drains in-flight
requests, stops the metrics listener, and stops the health listener last, per the
[graceful shutdown standard](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/graceful-shutdown.md).
The data-plane listener closes when the drain starts. A response whose headers
are written during the drain carries `Connection: close`. When the drain does not finish within
`SHUTDOWN_TIMEOUT`, the service closes the remaining connections on all
listeners and exits with an error.

Kubernetes removes a terminating pod from Service endpoints asynchronously, so
traffic can still arrive after `SIGTERM`. As in the HyperFleet API chart, the
deployment must delay `SIGTERM` with a `preStop` sleep (5s), and set
`terminationGracePeriodSeconds` above the `preStop` sleep plus
`SHUTDOWN_TIMEOUT`, with a margin.

## Layout

```text
cmd/hyperfleet-desire-store/  # entry point, serve and version subcommands
internal/config/              # flags, environment, validation, logger wiring
internal/auth/                # Wristband validation; authtest/ mints Wristbands for tests
internal/handlers/            # HTTP: routes, middleware, request decoding, responses
internal/services/            # business rules: caller permissions, store calls, store errors
internal/presenters/          # stored desires to REST v1 wire shapes, list cursors
openapi/                      # oapi-codegen config and overlay; make generate copies the contract here
pkg/api/openapi/              # generated REST v1 types and client (not committed)
internal/errors/              # ServiceError and its RFC 9457 problem form
internal/metrics/             # Prometheus registry and request metrics, see docs/metrics.md
internal/server/              # data plane, health and metrics listeners, shutdown
charts/                       # Helm chart, Grafana dashboard and chart README
scripts/                      # test-helm.sh: chart render tests
tools/                        # pinned Go tools (golangci-lint, yq, oapi-codegen, helm-docs, kubeconform)
.tekton/                      # Konflux PipelineRuns for the image and chart builds
.hyperfleet.yaml              # repository type, read by HyperFleet tooling
```

## Checks

Every change is expected to pass:

| Check | How to run it |
| --- | --- |
| Unit tests | `make test` |
| Lint | `make lint` |
| Formatting | `make fmt-check` |
| Vet | `make vet` |
| Konflux PipelineRuns parse | `make validate-tekton` |
| Helm chart lint, render and assertions | `make test-helm` |
| Commit message convention | `pre-commit install`, then the `commit-msg` hook |

Prow setup is pending in [HYPERFLEET-1918](https://redhat.atlassian.net/browse/HYPERFLEET-1918).
Once configured, the presubmits will run `make lint`, `make test`, and the
`hyperfleet-commitlint` check, in line with the [repository creation guidelines](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/docs/repository-creation-guidelines.md).
Until then, run these checks locally; their Makefile targets do not mean CI is active.

## Container image

```bash
make image                        # build locally
make image-push IMAGE_TAG=v0.1.0  # build and push
```

The build is FIPS-compliant (`CGO_ENABLED=1`, `GOEXPERIMENT=boringcrypto`) and
runs as UID `65532`, per the
[container image standard](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/container-image-standard.md).

## Konflux

Konflux builds the image on every push to `main` and on every version tag.
The `.tekton/` PipelineRuns are written against these coordinates:

| Item | Value |
| --- | --- |
| Konflux Application | `hyperfleet` |
| Konflux Component | `hyperfleet-desire-store` |
| Build image | `quay.io/redhat-user-workloads/hyperfleet-tenant/hyperfleet/hyperfleet-desire-store` |
| Released image | `quay.io/redhat-services-prod/hyperfleet-tenant/hyperfleet/hyperfleet-desire-store` |
| Build PipelineRun | `hyperfleet-desire-store-on-push`, `hyperfleet-desire-store-on-tag` |
| Service account | `build-pipeline-hyperfleet-desire-store` |

The Component registration is not done yet. Until it is, the PipelineRuns stay
inert: Pipelines as Code only matches them once Konflux knows the Component,
and the build pipeline service account does not exist. Registration happens in
the `hyperfleet-tenant` namespace and requires:

1. A `Component` for `github.com/openshift-hyperfleet/hyperfleet-desire-store` in the `hyperfleet` Application.
2. A Pipeline-as-Code repository and webhook so push and tag events reach the two PipelineRuns.
3. The `build-pipeline-hyperfleet-desire-store` service account and its Quay push secret.
4. The Slack webhook Secret the `finally` task reads — the same `hyperfleet-slack-webhook-notification-secret` used by the other components, so it may already exist.
5. A first green build on `main`, then a `vX.Y.Z` tag that produces a released image through the release pipeline.

The chart PipelineRuns (`.tekton/hyperfleet-desire-store-chart-push.yaml` and
`-chart-tag.yaml`) build the chart from `charts/` into an OCI artifact. They
are written against these coordinates:

| Item | Value |
| --- | --- |
| Konflux Application | `hyperfleet-charts` |
| Konflux Component | `hyperfleet-desire-store-chart` |
| Build artifact | `quay.io/redhat-user-workloads/hyperfleet-tenant/hyperfleet/hyperfleet-desire-store-chart` |
| Service account | `build-pipeline-hyperfleet-desire-store-chart` |

They use the same push and tag triggers as the image PipelineRuns. Register
the `hyperfleet-desire-store-chart` Component and create its service account
together with the image Component, before enabling repository events.
What Pipelines as Code does with a PipelineRun whose Component is missing is
`[unverified]`.

## Helm chart

`charts/` holds the Helm chart. [charts/README.md](charts/README.md) has the
install command and the values. The image registry, repository and tag, and six
service settings (see [docs/config.md](docs/config.md)), have no default; the
render fails while one is empty.

The chart does not wire database credentials yet; pass them with `extraEnv` or
`extraEnvFrom`. The ServiceMonitor, PrometheusRule and Grafana dashboard are
off by default. See [docs/metrics.md](docs/metrics.md#scraping) and
[docs/alerts.md](docs/alerts.md).

## License

Apache License 2.0. See [LICENSE](LICENSE).
