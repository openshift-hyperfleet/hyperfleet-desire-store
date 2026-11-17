# Konflux build pipelines

Two PipelineRuns build the Desire Store API image:

| PipelineRun | Trigger |
| --- | --- |
| `hyperfleet-desire-store-on-push` | Push to `main` |
| `hyperfleet-desire-store-on-tag` | Version tag (`vX.Y.Z` or `vX.Y.Z-rcN`) |

The chart PipelineRuns (`*-chart-push.yaml`, `*-chart-tag.yaml`) are added with
the Helm chart, so this directory holds image builds only.

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

The PipelineRuns are inert until the Component is registered in Konflux. See
the Konflux section of the [README](../README.md) for the registration steps:
Component, Pipelines as Code repository, `build-pipeline-hyperfleet-desire-store`
service account, and the Slack Secret.
