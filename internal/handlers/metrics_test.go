package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/auth"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/errors"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/metrics"
)

const (
	requestsTotal   = "hyperfleet_desire_store_requests_total"
	requestDuration = "hyperfleet_desire_store_request_duration_seconds"
	errorsTotal     = "hyperfleet_desire_store_errors_total"
	metricPrefix    = "hyperfleet_desire_store_"
	labelErrorCode  = "error_code"
	labelMethod     = "method"
	labelPath       = "path"
	labelStatus     = "status_code"

	listRoute   = desiresPath
	desireRoute = desiresPath + "/{id}"
	statusRoute = desiresPath + "/{id}/status"
)

type labelSet map[string]string

func gather(t *testing.T, a *testAPI) []*dto.MetricFamily {
	t.Helper()
	recorder := httptest.NewRecorder()
	a.metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	parser := expfmt.NewTextParser(model.UTF8Validation)
	parsed, err := parser.TextToMetricFamilies(recorder.Body)
	if err != nil {
		t.Fatalf("parsing the scrape: %v", err)
	}
	families := make([]*dto.MetricFamily, 0, len(parsed))
	for _, family := range parsed {
		families = append(families, family)
	}
	return families
}

func labelsOf(metric *dto.Metric) labelSet {
	labels := labelSet{}
	for _, label := range metric.GetLabel() {
		labels[label.GetName()] = label.GetValue()
	}
	return labels
}

func hasLabels(have, want labelSet) bool {
	for name, value := range want {
		if have[name] != value {
			return false
		}
	}
	return true
}

// sampleValue returns the counter value, or the histogram sample count, of the
// series of family that carries all the given labels.
func sampleValue(t *testing.T, a *testAPI, family string, want labelSet) float64 {
	t.Helper()
	var total float64
	for _, f := range gather(t, a) {
		if f.GetName() != family {
			continue
		}
		for _, metric := range f.GetMetric() {
			if !hasLabels(labelsOf(metric), want) {
				continue
			}
			if metric.GetHistogram() != nil {
				total += float64(metric.GetHistogram().GetSampleCount())
			} else {
				total += metric.GetCounter().GetValue()
			}
		}
	}
	return total
}

func expectCount(t *testing.T, a *testAPI, family string, labels labelSet, want float64) {
	t.Helper()
	if got := sampleValue(t, a, family, labels); got != want {
		t.Fatalf("%s%v = %v, want %v", family, labels, got, want)
	}
}

func TestMetricsCountSuccessfulRequests(t *testing.T) {
	a := newTestAPI(t)
	a.do(t, http.MethodGet, listPath(), a.applier(t), "").expect(t, http.StatusOK, "")

	labels := labelSet{labelMethod: http.MethodGet, labelPath: listRoute, labelStatus: "200"}
	expectCount(t, a, requestsTotal, labels, 1)
	expectCount(t, a, requestDuration, labels, 1)
	expectCount(t, a, errorsTotal, labelSet{}, 0)
}

func TestMetricsCountOwnerAndVersionConflicts(t *testing.T) {
	a := newTestAPI(t)
	a.do(t, http.MethodPut, desirePath(applyID), a.adapter(t), applyBody("coredns")).expect(t, http.StatusCreated, "")
	a.do(t, http.MethodPut, desirePath(applyID), a.otherAdapter(t), applyBody("coredns")).
		expect(t, http.StatusConflict, errors.CodeOwnerConflict)

	expectCount(t, a, requestsTotal, labelSet{labelMethod: http.MethodPut, labelPath: desireRoute, labelStatus: "409"}, 1)
	expectCount(t, a, errorsTotal,
		labelSet{labelMethod: http.MethodPut, labelPath: desireRoute, labelErrorCode: errors.CodeOwnerConflict}, 1)

	a.do(t, http.MethodPut, statusPath(applyID), a.applier(t), appliedStatus(1)).expect(t, http.StatusNoContent, "")
	a.do(t, http.MethodPut, statusPath(applyID), a.applier(t), appliedStatus(1)).
		expect(t, http.StatusConflict, errors.CodeVersionConflict)
	expectCount(t, a, errorsTotal,
		labelSet{labelMethod: http.MethodPut, labelPath: statusRoute, labelErrorCode: errors.CodeVersionConflict}, 1)
	expectCount(t, a, requestsTotal, labelSet{labelMethod: http.MethodPut, labelPath: statusRoute, labelStatus: "204"}, 1)
}

func TestMetricsCountRejectedRequestsWithTheRoute(t *testing.T) {
	a := newTestAPI(t)
	a.do(t, http.MethodGet, listPath(), "", "").expect(t, http.StatusUnauthorized, auth.CodeMissing)

	expectCount(t, a, requestsTotal, labelSet{labelMethod: http.MethodGet, labelPath: listRoute, labelStatus: "401"}, 1)
	expectCount(t, a, errorsTotal, labelSet{labelPath: listRoute, labelErrorCode: auth.CodeMissing}, 1)
}

func TestMetricsLabelUnmatchedRequests(t *testing.T) {
	a := newTestAPI(t)
	a.do(t, http.MethodGet, "/nowhere/"+testPartition, a.applier(t), "").
		expect(t, http.StatusNotFound, errors.CodeEndpointNotFound)
	expectCount(t, a, requestsTotal,
		labelSet{labelMethod: http.MethodGet, labelPath: unmatchedPath, labelStatus: "404"}, 1)
	expectCount(t, a, errorsTotal,
		labelSet{labelPath: unmatchedPath, labelErrorCode: errors.CodeEndpointNotFound}, 1)

	a.do(t, http.MethodPost, listPath(), a.applier(t), minimalWriteBody).expect(t, http.StatusMethodNotAllowed, "")
	expectCount(t, a, requestsTotal,
		labelSet{labelMethod: http.MethodPost, labelPath: unmatchedPath, labelStatus: "405"}, 1)
	expectCount(t, a, errorsTotal, labelSet{labelMethod: http.MethodPost, labelPath: unmatchedPath}, 0)
}

func TestMetricsReportAnUnknownMethodAsOther(t *testing.T) {
	a := newTestAPI(t)
	a.do(t, "FOO", listPath(), a.applier(t), "")

	expectCount(t, a, requestsTotal, labelSet{labelMethod: "OTHER"}, 1)
	expectCount(t, a, requestsTotal, labelSet{labelMethod: "FOO"}, 0)
}

func TestMetricsCarryNoPartition(t *testing.T) {
	a := newTestAPI(t)
	adapter := a.adapter(t)
	const second = "mc-02"
	a.do(t, http.MethodGet, listPath(), a.applier(t), "")
	a.do(t, http.MethodGet, partitionDesirePath(second, applyID), a.token(t, auth.ClassFleetScoped, adapterA, second), "")
	a.do(t, http.MethodPut, desirePath(applyID), adapter, applyBody("coredns"))

	for _, family := range gather(t, a) {
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if strings.Contains(label.GetValue(), testPartition) || strings.Contains(label.GetValue(), second) {
					t.Fatalf("%s label %s = %q carries a partition", family.GetName(), label.GetName(), label.GetValue())
				}
			}
		}
	}
}

func TestNewRouterRejectsNilMetrics(t *testing.T) {
	if _, err := NewRouter(nil, nil, (*metrics.Metrics)(nil), nil); err == nil {
		t.Fatal("NewRouter() accepted nil metrics")
	}
}

// TestMetricsDocumented ties docs/metrics.md to the names the service exposes:
// a rename fails here until the document, which dashboards and alerts are
// built on, changes too.
func TestMetricsDocumented(t *testing.T) {
	a := newTestAPI(t)
	a.do(t, http.MethodGet, listPath(), a.applier(t), "").expect(t, http.StatusOK, "")
	a.do(t, http.MethodPut, desirePath(applyID), a.adapter(t), applyBody("coredns")).expect(t, http.StatusCreated, "")
	a.do(t, http.MethodPut, desirePath(applyID), a.otherAdapter(t), applyBody("coredns")).
		expect(t, http.StatusConflict, errors.CodeOwnerConflict)
	a.do(t, http.MethodGet, "/nowhere", a.applier(t), "").expect(t, http.StatusNotFound, errors.CodeEndpointNotFound)

	doc, err := os.ReadFile("../../docs/metrics.md")
	if err != nil {
		t.Fatalf("reading the metrics document: %v", err)
	}
	text := string(doc)
	seen := 0
	for _, family := range gather(t, a) {
		if !strings.HasPrefix(family.GetName(), metricPrefix) {
			continue
		}
		seen++
		if !strings.Contains(text, "`"+family.GetName()+"`") {
			t.Errorf("metric %s is not documented in docs/metrics.md", family.GetName())
		}
		for _, label := range family.GetMetric()[0].GetLabel() {
			if !strings.Contains(text, "`"+label.GetName()+"`") {
				t.Errorf("label %s of %s is not documented in docs/metrics.md", label.GetName(), family.GetName())
			}
		}
	}
	if seen != 4 {
		t.Fatalf("gathered %d %s* families, want 4", seen, metricPrefix)
	}
}
