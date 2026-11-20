# Konflux build pipelines

Two PipelineRuns build the Desire Store API image:

| PipelineRun | Trigger |
| --- | --- |
| `hyperfleet-desire-store-on-push` | Push to `main` |
| `hyperfleet-desire-store-on-tag` | Version tag (`vX.Y.Z` or `vX.Y.Z-rcN`) |

Two more PipelineRuns build the Helm chart from `charts/` into an OCI artifact:

| PipelineRun | Trigger |
| --- | --- |
| `hyperfleet-desire-store-chart-on-push` | Push to `main` |
| `hyperfleet-desire-store-chart-on-tag` | Version tag (`vX.Y.Z` or `vX.Y.Z-rcN`) |

They belong to the `hyperfleet-desire-store-chart` Component in the
`hyperfleet-charts` Application and use the service account
`build-pipeline-hyperfleet-desire-store-chart`.

Each inline `pipelineSpec` has a `slack-webhook-notification` task in `finally`.
It runs only when `$(tasks.status)` is `Failed`. The Slack message links the
repository, commit, and failed PipelineRun with readable text. A successful
PipelineRun skips this task. A failure before a PipelineRun starts, including a
Pipelines as Code admission failure, cannot trigger it.

The task reads key `hyperfleet-slack-webhook-url` from Secret
`hyperfleet-slack-webhook-notification-secret` in `hyperfleet-tenant` on
`kflux-prd-rh02`, shared with the other HyperFleet components. The webhook value
must never be committed or printed in logs.

## Before these run

Register both Components (`hyperfleet-desire-store` and
`hyperfleet-desire-store-chart`) and create both service accounts together,
before enabling repository events. What Pipelines as Code does with a
PipelineRun whose Component is missing is unverified. See
the Konflux section of the [README](../README.md) for the registration steps:
the Components, the Pipelines as Code repository, the
`build-pipeline-hyperfleet-desire-store` and
`build-pipeline-hyperfleet-desire-store-chart` service accounts, and the Slack
Secret.
