# Configuration

The `serve` command reads configuration once at startup. Changes require a
restart; runtime reload is not supported. The `version` command does not require
service configuration.

## Sources and precedence

The scaffold supports command-line flags, environment variables, and built-in
defaults, in descending order of precedence. Configuration files, remote
configuration, and a `--config` flag are not implemented in this scaffold.

An unset or empty environment variable uses the built-in default. Environment
values are parsed before flags are bound, so a malformed `SHUTDOWN_TIMEOUT`
must be corrected in the environment even if a flag would override it.

## Options

| Flag | Environment variable | Default | Meaning and validation |
| --- | --- | --- | --- |
| `--server-addr` | `HYPERFLEET_SERVER_ADDR` | `:8000` | HTTP data-plane listen address. Must not be empty; an invalid or occupied address fails listener startup. REST v1 routes are not implemented yet. |
| `--health-addr` | `HYPERFLEET_HEALTH_ADDR` | `:8080` | HTTP listen address for `/healthz` and `/readyz`. Must not be empty; an invalid or occupied address fails listener startup. Port `8080` is the platform default for probes. |
| `--shutdown-timeout` | `SHUTDOWN_TIMEOUT` | `20s` | Positive duration bounding graceful shutdown, including in-flight request drain. Keep it below the pod's `terminationGracePeriodSeconds`. |
| `--log-level`, `-l` | `HYPERFLEET_LOG_LEVEL` | `info` | Minimum log severity: `debug`, `info`, `warn`, or `error`. |
| `--log-format`, `-f` | `HYPERFLEET_LOG_FORMAT` | `json` | Log encoding: `json` or `text`. |
| `--log-output` | `HYPERFLEET_LOG_OUTPUT` | `stdout` | Log destination: `stdout` or `stderr`. |

`SHUTDOWN_TIMEOUT` is intentionally unprefixed, following the
[graceful shutdown standard](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/graceful-shutdown.md).
There is no `HYPERFLEET_SHUTDOWN_TIMEOUT` alias.

The environment accepts both Go duration strings (`20s`, `1h30m`) and bare
integer seconds (`20`). The `--shutdown-timeout` flag accepts Go duration strings
and should include a unit, for example `--shutdown-timeout=30s`.

Malformed durations, non-positive shutdown timeouts, and unsupported logging
values fail startup rather than being silently ignored.

## Example

```bash
HYPERFLEET_SERVER_ADDR=:9000 SHUTDOWN_TIMEOUT=30 \
  ./bin/hyperfleet-desire-store serve --log-format=text --shutdown-timeout=25s
```

This listens on `:9000` for the data plane and `:8080` for health checks, logs
text to stdout at `info` level, and uses the flag's `25s` shutdown budget instead
of the environment's `30` seconds.

## References

- [HyperFleet configuration standard](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/configuration.md)
- [HyperFleet logging specification](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/logging-specification.md)
