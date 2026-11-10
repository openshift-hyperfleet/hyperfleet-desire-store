# HyperFleet Desire Store API

The Desire Store API transports desires from HyperFleet adapters to appliers.
It is a rebuildable delivery channel, not a source of truth: if the store is
lost, an adapter recomputes and rewrites its desires from API state.

The service is part of the Desire Store design:

- [Desire Store Detailed Design](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/docs/desire-store-dd.md) — REST v1 contract, authorization, and failure behavior.
- [Desire Store HLD](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/docs/desire-store-hld.md) — system context and topology.

## Status

This repository currently holds the service scaffold: the process starts,
serves the standard health endpoints, shuts down gracefully, and builds a
FIPS-compliant image. The REST v1 routes are not implemented yet; the data
plane listener exists so the deployment and gateway wiring can be built
against the final shape.

## Quick start

```bash
make build      # bin/hyperfleet-desire-store
make run        # serve with text logs on :8000 and :8080
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
| `8000` | `/api/hyperfleet/desire-store/v1/...` | Desire Store REST v1 data plane, not implemented yet. |

Both follow the [health endpoints standard](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/health-endpoints.md).

### Configuration

Flags win over environment variables, and environment variables win over the
defaults. A malformed value fails startup instead of being ignored.

| Flag | Environment variable | Default |
| --- | --- | --- |
| `--server-addr` | `HYPERFLEET_SERVER_ADDR` | `:8000` |
| `--health-addr` | `HYPERFLEET_HEALTH_ADDR` | `:8080` |
| `--shutdown-timeout` | `SHUTDOWN_TIMEOUT` | `20s` |
| `--log-level`, `-l` | `HYPERFLEET_LOG_LEVEL` | `info` |
| `--log-format`, `-f` | `HYPERFLEET_LOG_FORMAT` | `json` |
| `--log-output` | `HYPERFLEET_LOG_OUTPUT` | `stdout` |

Settings and naming follow the [configuration standard](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/configuration.md)
and the [logging specification](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/logging-specification.md).

On `SIGTERM` or `SIGINT` the service marks itself not ready, drains in-flight
requests, and stops the health listener last, per the
[graceful shutdown standard](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/graceful-shutdown.md).

## Layout

```text
cmd/hyperfleet-desire-store/  # entry point, serve and version subcommands
internal/config/              # flags, environment, validation, logger wiring
internal/server/              # data plane and health listeners, shutdown
tools/                        # pinned Go tools (golangci-lint, yq)
.tekton/                      # Konflux PipelineRuns for the image build
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
| Commit message convention | `pre-commit install`, then the `commit-msg` hook |

The Prow presubmits for this repository run `make lint` and `make test` and the
`hyperfleet-commitlint` check, in line with the [repository creation guidelines](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/docs/repository-creation-guidelines.md).

## Container image

```bash
make image                        # build locally
make image-push IMAGE_TAG=v0.1.0  # push
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

Chart pipelines (`.tekton/*-chart-*.yaml`) are added with the Helm chart.

## License

Apache License 2.0. See [LICENSE](LICENSE).
