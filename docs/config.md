# Configuration

The `serve` command reads configuration once at startup. Changes require a
restart; runtime reload is not supported. The `version` command does not require
service configuration.

## Sources and precedence

The scaffold supports command-line flags, environment variables, and built-in
defaults, in descending order of precedence. Configuration files, remote
configuration, and a `--config` flag are not implemented in this scaffold.

An unset or empty environment variable uses the built-in default.
`serve` reads an environment variable only when its flag is not given. A flag
therefore also overrides a malformed variable. An environment variable uses the
syntax of its flag, except `SHUTDOWN_TIMEOUT` (see below).

## Options

| Flag | Environment variable | Default | Meaning and validation |
| --- | --- | --- | --- |
| `--server-addr` | `HYPERFLEET_SERVER_ADDR` | `:8000` | HTTP data-plane listen address. Must not be empty; an invalid or occupied address fails listener startup. REST v1 routes are not mounted until the Postgres store lands. |
| `--health-addr` | `HYPERFLEET_HEALTH_ADDR` | `:8080` | HTTP listen address for `/healthz` and `/readyz`. Must not be empty; an invalid or occupied address fails listener startup. Port `8080` is the platform default for probes. |
| `--shutdown-timeout` | `SHUTDOWN_TIMEOUT` | `20s` | Positive duration bounding graceful shutdown, including in-flight request drain. Keep it below the pod's `terminationGracePeriodSeconds`. |
| `--log-level`, `-l` | `HYPERFLEET_LOG_LEVEL` | `info` | Minimum log severity: `debug`, `info`, `warn`, or `error`. |
| `--log-format`, `-f` | `HYPERFLEET_LOG_FORMAT` | `json` | Log encoding: `json` or `text`. |
| `--log-output` | `HYPERFLEET_LOG_OUTPUT` | `stdout` | Log destination: `stdout` or `stderr`. |
| `--partition-claim` | `HYPERFLEET_PARTITION_CLAIM` | none, required | Wristband claim that carries the caller's partition. |
| `--wristband-issuer` | `HYPERFLEET_WRISTBAND_ISSUER` | none, required | `iss` claim every Wristband must carry. |
| `--wristband-audience` | `HYPERFLEET_WRISTBAND_AUDIENCE` | none, required | `aud` claim every Wristband must carry. |
| `--wristband-jwks-url` | `HYPERFLEET_WRISTBAND_JWKS_URL` | none, required | Absolute `http` or `https` URL of the key set that signs Wristbands, without user information. |
| `--wristband-jwks-ca-file` | `HYPERFLEET_WRISTBAND_JWKS_CA_FILE` | none: the system trust store | PEM CA bundle that is trusted in addition to the system trust store when the key set is fetched. The file is read only when the key set is fetched, so a missing file does not fail startup yet. |
| `--wristband-algorithms` | `HYPERFLEET_WRISTBAND_ALGORITHMS` | `ES256` | Accepted signing algorithms, separated by commas without spaces, for example `ES256,ES384`. An unknown algorithm and `none` fail startup. |
| `--sweeper-identity` | `HYPERFLEET_SWEEPER_IDENTITY` | none, required | `hf_identity` of the centralized sweeper, in the form `system:serviceaccount:<namespace>:<name>`. Every other fleet-scoped caller gets the Adapter's permissions. |
| `--db-max-concurrency` | `HYPERFLEET_DB_MAX_CONCURRENCY` | none, required | Connection pool size for the Postgres store. Must be at least `1`. Not used until the Postgres store lands. |

`SHUTDOWN_TIMEOUT` is intentionally unprefixed, following the
[graceful shutdown standard](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/graceful-shutdown.md).
There is no `HYPERFLEET_SHUTDOWN_TIMEOUT` alias.

The environment accepts both Go duration strings (`20s`, `1h30m`) and bare
integer seconds (`20`). The `--shutdown-timeout` flag accepts Go duration strings
and should include a unit, for example `--shutdown-timeout=30s`.

Malformed durations, non-positive shutdown timeouts, unsupported logging
values, and missing or invalid required settings fail startup. They are not
silently ignored.

`serve` validates the Wristband, sweeper, and database settings at startup.
Nothing uses them until the REST v1 routes are mounted.

## Example

```bash
HYPERFLEET_SERVER_ADDR=:9000 SHUTDOWN_TIMEOUT=30 \
  ./bin/hyperfleet-desire-store serve --log-format=text --shutdown-timeout=25s
```

The example needs the required settings from the table in the environment too.
It listens on `:9000` for the data plane and `:8080` for health checks. It logs
text to stdout at `info` level. It uses the flag's `25s` shutdown budget instead
of the environment's `30` seconds.

## References

- [HyperFleet configuration standard](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/configuration.md)
- [HyperFleet logging specification](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/standards/logging-specification.md)
