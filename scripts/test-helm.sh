#!/usr/bin/env bash
set -euo pipefail

CHART_DIR="charts"
RELEASE_NAME="test-release"

HELM="${HELM:-helm}"
VALUES_FILE="charts/ci/default-values.yaml"

KUBECONFORM="${KUBECONFORM:-kubeconform}"
KUBECONFORM_FLAGS=(
  -strict
  -kubernetes-version 1.30.0
  -schema-location default
  -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'
)

PASSED=0
FAILED=0
SCENARIO_FAILED=0

render() {
  "$HELM" template "$RELEASE_NAME" "$CHART_DIR" --values "$VALUES_FILE" "$@"
}

run_test() {
  SCENARIO_FAILED=0
  echo ""
  echo "Testing $1..."
}

pass() {
  if [ "$SCENARIO_FAILED" -eq 0 ]; then
    echo "  ✓ $1"
    PASSED=$((PASSED + 1))
  fi
}

fail() {
  echo "  ✗ FAIL: $1"
  FAILED=$((FAILED + 1))
  SCENARIO_FAILED=1
}

assert_contains() {
  local input="$1" pattern="$2" msg="$3"
  if ! grep -Fq -- "$pattern" <<<"$input"; then
    fail "$msg"
  fi
}

assert_not_contains() {
  local input="$1" pattern="$2" msg="$3"
  if grep -Fq -- "$pattern" <<<"$input"; then
    fail "$msg"
  fi
}

# assert_line matches a whole line, so a substring elsewhere does not satisfy it.
assert_line() {
  local input="$1" line="$2" msg="$3"
  if ! grep -Fxq -- "$line" <<<"$input"; then
    fail "$msg"
  fi
}

# assert_kubeconform <rendered manifests>
assert_kubeconform() {
  if $KUBECONFORM "${KUBECONFORM_FLAGS[@]}" <<<"$1" > /dev/null; then
    pass "kubeconform accepts the render"
  else
    fail "kubeconform rejected the render"
  fi
}

# assert_port_follows <helm --set arg> <ADDR env name> <port>
assert_port_follows() {
  local setting="$1" addr="$2" port="$3" out
  out=$(render --set "$setting=$port")
  assert_contains "$out" "$addr: \":$port\"" "$addr does not follow $setting"
  assert_contains "$out" "containerPort: $port" "container port does not follow $setting"
  assert_line "$out" "      port: $port" "Service does not use $setting"
}

# assert_render_fails <expected message> <helm --set args...>
assert_render_fails() {
  local expected="$1" out
  shift
  if out=$(render "$@" 2>&1); then
    fail "render succeeded, expected failure containing: $expected"
  else
    assert_contains "$out" "$expected" "failure message does not contain: $expected"
  fi
}

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Testing Helm chart..."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

command -v "$HELM" > /dev/null 2>&1 || {
  echo "Required tool not found: $HELM"
  exit 1
}

run_test "lint"
if "$HELM" lint "$CHART_DIR" --values "$VALUES_FILE" > /dev/null; then
  pass "helm lint passes"
else
  fail "helm lint failed"
fi

run_test "default render"
out=$(render)
assert_kubeconform "$out"
assert_contains "$out" "kind: Deployment" "Deployment missing"
assert_line "$out" "kind: Service" "Service missing"
assert_contains "$out" "kind: ConfigMap" "ConfigMap missing"
assert_contains "$out" "kind: ServiceAccount" "ServiceAccount missing"
assert_contains "$out" "checksum/config:" "checksum/config annotation missing"
assert_contains "$out" "readOnlyRootFilesystem: true" "root filesystem is not read-only"
assert_contains "$out" "allowPrivilegeEscalation: false" "privilege escalation is not disabled"
assert_contains "$out" "path: /healthz" "liveness path missing"
assert_contains "$out" "path: /readyz" "readiness path missing"
assert_contains "$out" "containerPort: 8000" "api container port missing"
assert_contains "$out" "containerPort: 8080" "health container port missing"
assert_contains "$out" "containerPort: 9090" "metrics container port missing"
assert_contains "$out" 'HYPERFLEET_SERVER_ADDR: ":8000"' "default server address wrong"
assert_contains "$out" 'HYPERFLEET_HEALTH_ADDR: ":8080"' "default health address wrong"
assert_contains "$out" 'HYPERFLEET_METRICS_ADDR: ":9090"' "default metrics address wrong"
assert_not_contains "$out" "kind: ServiceMonitor" "ServiceMonitor rendered by default"
assert_not_contains "$out" "kind: PrometheusRule" "PrometheusRule rendered by default"
assert_not_contains "$out" "grafana_dashboard" "dashboard rendered by default"
assert_not_contains "$out" "kind: PodDisruptionBudget" "PDB rendered by default"
assert_not_contains "$out" "SHUTDOWN_TIMEOUT" "empty shutdownTimeout is rendered"
assert_not_contains "$out" "HYPERFLEET_LOG_LEVEL" "empty log level is rendered"
pass "default render has the expected resources, ports, probes and addresses"

run_test "optional settings"
out=$(render --set config.shutdownTimeout=30s --set config.log.level=debug)
assert_contains "$out" 'SHUTDOWN_TIMEOUT: "30s"' "shutdownTimeout not rendered"
assert_contains "$out" 'HYPERFLEET_LOG_LEVEL: "debug"' "log level not rendered"
pass "optional settings render only when set"

run_test "shutdown budget"
assert_render_fails "terminationGracePeriodSeconds" --set config.shutdownTimeout=60s
assert_render_fails "terminationGracePeriodSeconds" --set config.shutdownTimeout=1m30s
out=$(render --set config.shutdownTimeout=60s --set terminationGracePeriodSeconds=70)
assert_contains "$out" 'SHUTDOWN_TIMEOUT: "60s"' "shutdownTimeout not rendered"
# The default grace period is 45s and the default preStop sleep 5s: 40s is at the limit.
assert_render_fails "terminationGracePeriodSeconds" --set config.shutdownTimeout=40s
assert_render_fails "terminationGracePeriodSeconds" --set config.shutdownTimeout=40000ms
render --set config.shutdownTimeout=39s > /dev/null || fail "39s is within the grace period"
# Every form time.ParseDuration accepts is checked; anything else fails.
assert_render_fails "terminationGracePeriodSeconds" --set config.shutdownTimeout=+40s
assert_render_fails "terminationGracePeriodSeconds" --set config.shutdownTimeout=40000000us
assert_render_fails "terminationGracePeriodSeconds" --set config.shutdownTimeout=.7m
render --set config.shutdownTimeout=39999999999ns > /dev/null || fail "39999999999ns is within the grace period"
assert_render_fails "non-negative Go duration" --set config.shutdownTimeout=-5s
assert_render_fails "non-negative Go duration" --set config.shutdownTimeout=20sec
# Without a preStop hook, nothing is added to the timeout.
render --set lifecycle=null --set config.shutdownTimeout=44s > /dev/null || fail "44s without preStop is within the grace period"
# Each sleep shape the helper reads counts toward the budget.
assert_render_fails "terminationGracePeriodSeconds" --set-json 'lifecycle={"preStop":{"exec":{"command":["sleep","30"]}}}'
assert_render_fails "terminationGracePeriodSeconds" --set-json 'lifecycle={"preStop":{"exec":{"command":["/bin/sh","-c","sleep 30"]}}}'
assert_render_fails "terminationGracePeriodSeconds" --set lifecycle.preStop.exec=null --set-json 'lifecycle.preStop.sleep={"seconds":30}'
out=$(render --set lifecycle.preStop.exec=null --set-json 'lifecycle.preStop.sleep={"seconds":10}')
assert_contains "$out" "seconds: 10" "native preStop sleep not rendered"
# A hook whose duration the helper cannot read fails instead of passing unchecked.
assert_render_fails "lifecycle.preStop must be" --set lifecycle.preStop.exec=null --set-json 'lifecycle.preStop.httpGet={"path":"/drain","port":8080}'
assert_render_fails "lifecycle.preStop must be" --set-json 'lifecycle={"preStop":{"exec":{"command":["/bin/sh","-c","sleep 5; kill 1"]}}}'
pass "the grace period must exceed the preStop sleep plus the shutdown timeout"

run_test "extra volumes"
out=$(render --set extraVolumes[0].name=ca-certs \
  --set extraVolumes[0].configMap.name=ca-bundle \
  --set extraVolumeMounts[0].name=ca-certs \
  --set extraVolumeMounts[0].mountPath=/etc/hyperfleet/ca)
assert_contains "$out" "name: ca-bundle" "extra volume missing"
assert_contains "$out" "mountPath: /etc/hyperfleet/ca" "extra volume mount missing"
pass "extra volumes and mounts render"

run_test "podDisruptionBudget"
out=$(render --set podDisruptionBudget.enabled=true)
assert_contains "$out" "kind: PodDisruptionBudget" "PDB missing"
pass "PDB renders when enabled"

run_test "serviceMonitor"
out=$(render --set monitoring.serviceMonitor.enabled=true)
assert_contains "$out" "kind: ServiceMonitor" "ServiceMonitor missing"
assert_contains "$out" "port: metrics" "ServiceMonitor port missing"
assert_not_contains "$out" "kind: PrometheusRule" "PrometheusRule rendered with only the ServiceMonitor on"
assert_contains "$out" "honorLabels: true" "honorLabels default is not true"
assert_contains "$out" "targetLabel: component" "component target label missing"
assert_contains "$out" "replacement: desire-store" "component label value missing"
pass "ServiceMonitor renders on the metrics port"

run_test "serviceMonitor namespaceSelector"
out=$(render --set monitoring.serviceMonitor.enabled=true --set-json 'monitoring.serviceMonitor.namespaceSelector={"any":true}')
assert_contains "$out" "any: true" "namespaceSelector value not rendered"
pass "namespaceSelector comes from values"

run_test "prometheusRule with serviceMonitor"
out=$(render --set monitoring.serviceMonitor.enabled=true --set monitoring.prometheusRule.enabled=true)
assert_contains "$out" "kind: PrometheusRule" "PrometheusRule missing"
assert_contains "$out" "alert: HyperFleetDesireStoreDown" "Down alert missing with the ServiceMonitor on"
assert_contains "$out" "alert: HyperFleetDesireStoreHighErrorRatio" "error ratio alert missing"
assert_contains "$out" "alert: HyperFleetDesireStoreHighLatency" "latency alert missing"
assert_contains "$out" 'service="test-release-hyperfleet-desire-store"' "alert queries are not scoped to the service"
assert_contains "$out" 'namespace="default"' "alert queries are not scoped to the namespace"
pass "all three alerts render, scoped to the release"

run_test "prometheusRule without serviceMonitor"
out=$(render --set monitoring.prometheusRule.enabled=true)
assert_contains "$out" "kind: PrometheusRule" "PrometheusRule missing"
assert_not_contains "$out" "alert: HyperFleetDesireStoreDown" "Down alert rendered without the ServiceMonitor"
assert_contains "$out" "alert: HyperFleetDesireStoreHighErrorRatio" "error ratio alert missing"
pass "Down alert is absent while the ServiceMonitor is off"

run_test "alert thresholds"
out=$(render --set monitoring.prometheusRule.enabled=true \
  --set-json monitoring.prometheusRule.alerts.highErrorRatio.threshold=0.2 \
  --set-json monitoring.prometheusRule.alerts.highLatency.thresholdSeconds=2.5)
assert_contains "$out" ") > 0.2" "error ratio threshold value not used"
assert_contains "$out" ") > 2.5" "latency threshold value not used"
pass "thresholds come from values"

run_test "dashboard"
out=$(render --set monitoring.dashboard.enabled=true)
assert_contains "$out" 'grafana_dashboard: "1"' "dashboard label missing"
assert_contains "$out" "hyperfleet-desire-store.json" "dashboard key missing"
assert_contains "$out" 'label_values(up{component=\"desire-store\"}, namespace)' "namespace selector is not the component-scoped up query"
assert_contains "$out" 'label_values(up{component=\"desire-store\", namespace=~\"$namespace\"}, service)' "service selector is not the component-scoped up query"
pass "dashboard ConfigMap renders"

run_test "monitoring resources schema check"
out=$(render --set monitoring.serviceMonitor.enabled=true \
  --set monitoring.prometheusRule.enabled=true \
  --set monitoring.dashboard.enabled=true)
assert_kubeconform "$out"

run_test "port overrides"
assert_port_follows observability.healthPort HYPERFLEET_HEALTH_ADDR 8081
assert_port_follows observability.metricsPort HYPERFLEET_METRICS_ADDR 9191
assert_port_follows ports.api HYPERFLEET_SERVER_ADDR 8100
pass "listener addresses, container ports and Service follow the port values"

run_test "version label"
long_tag=$(printf 'a%.0s' $(seq 1 64))
out=$(render --set image.tag="$long_tag")
assert_contains "$out" "image: \"quay.io/openshift-hyperfleet/hyperfleet-desire-store:$long_tag\"" "image tag was changed"
assert_contains "$out" "app.kubernetes.io/version: \"${long_tag:0:63}\"" "version label is not cut to 63 characters"
out=$(render --set image.tag=v1.2.3)
assert_contains "$out" 'app.kubernetes.io/version: "v1.2.3"' "short tag label changed"
pass "a 64-character tag gives a valid label and an unchanged image"

run_test "required settings"
for setting in image.registry image.repository image.tag \
  config.partitionClaim config.dbMaxConcurrency config.sweeperIdentity \
  config.wristband.issuer config.wristband.audience config.wristband.jwksUrl; do
  assert_render_fails "$setting must be set" --set "$setting="
done
pass "each empty image value and required setting fails the render"

run_test "dbMaxConcurrency"
expected_error="config.dbMaxConcurrency must be set to an integer of at least 1"
for value in 0 -1; do
  assert_render_fails "$expected_error" --set "config.dbMaxConcurrency=$value"
done
for value in 0 -1 ten 5.0 9223372036854775808; do
  assert_render_fails "$expected_error" --set-string "config.dbMaxConcurrency=$value"
done
out=$(render --set config.dbMaxConcurrency=1)
assert_line "$out" '  HYPERFLEET_DB_MAX_CONCURRENCY: "1"' "dbMaxConcurrency integer value was not rendered"
out=$(render --set-string "config.dbMaxConcurrency= 5 ")
assert_line "$out" '  HYPERFLEET_DB_MAX_CONCURRENCY: "5"' "dbMaxConcurrency string value was not trimmed"
pass "the ConfigMap contains the validated positive integer"

run_test "schema"
# Helm <= 3.18 prints the schema path with dots, newer Helm with slashes.
if out=$(render --set-string monitoring.serviceMonitor.enabled=false 2>&1); then
  fail "render succeeded, expected a schema type error"
elif grep -Eq 'serviceMonitor[./]enabled' <<<"$out"; then
  pass "a quoted boolean is rejected by the schema"
else
  fail "failure message does not name serviceMonitor.enabled: $out"
fi

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Passed: $PASSED, Failed: $FAILED"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
[ "$FAILED" -eq 0 ]
