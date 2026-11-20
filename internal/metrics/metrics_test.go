package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestObserveRequestCountsAndLabels(t *testing.T) {
	m := New("v1.2.3", "abc123")
	m.ObserveRequest("GET", "/x/{id}", 200, "", 10*time.Millisecond)
	m.ObserveRequest("GET", "/x/{id}", 200, "", 20*time.Millisecond)
	m.ObserveRequest("PUT", "/x/{id}", 409, "HYPERFLEET-CNF-004", time.Millisecond)

	const (
		requests = "hyperfleet_desire_store_requests_total"
		errs     = "hyperfleet_desire_store_errors_total"
		constant = `component="desire-store"`
		ver      = `version="v1.2.3"`
	)
	want := "# HELP " + requests + " Total number of HTTP requests served by the data plane.\n" +
		"# TYPE " + requests + " counter\n" +
		requests + "{" + constant + `,method="GET",path="/x/{id}",status_code="200",` + ver + "} 2\n" +
		requests + "{" + constant + `,method="PUT",path="/x/{id}",status_code="409",` + ver + "} 1\n" +
		"# HELP " + errs + " Total number of requests answered with an Error Model problem, by error code.\n" +
		"# TYPE " + errs + " counter\n" +
		errs + "{" + constant + `,error_code="HYPERFLEET-CNF-004",method="PUT",path="/x/{id}",` + ver + "} 1\n"
	err := testutil.GatherAndCompare(m.registry, strings.NewReader(want), requests, errs)
	if err != nil {
		t.Fatal(err)
	}
}

func TestDurationHistogramUsesTheStandardBuckets(t *testing.T) {
	m := New("v", "c")
	m.ObserveRequest("GET", "/x", 200, "", 30*time.Millisecond)

	families, err := m.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "hyperfleet_desire_store_request_duration_seconds" {
			continue
		}
		histogram := family.GetMetric()[0].GetHistogram()
		if histogram.GetSampleCount() != 1 {
			t.Fatalf("sample count = %d, want 1", histogram.GetSampleCount())
		}
		standard := []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
		if got := len(histogram.GetBucket()); got != len(standard) {
			t.Fatalf("buckets = %d, want %d", got, len(standard))
		}
		for i, bucket := range histogram.GetBucket() {
			if bucket.GetUpperBound() != standard[i] {
				t.Fatalf("bucket %d = %v, want %v", i, bucket.GetUpperBound(), standard[i])
			}
		}
		return
	}
	t.Fatal("duration histogram not gathered")
}

func TestBuildInfoIsOneWithVersionAndCommit(t *testing.T) {
	m := New("v1.2.3", "abc123")
	families, err := m.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "hyperfleet_desire_store_build_info" {
			continue
		}
		metric := family.GetMetric()[0]
		if metric.GetGauge().GetValue() != 1 {
			t.Fatalf("build_info = %v, want 1", metric.GetGauge().GetValue())
		}
		labels := map[string]string{}
		for _, label := range metric.GetLabel() {
			labels[label.GetName()] = label.GetValue()
		}
		if labels["version"] != "v1.2.3" || labels["commit"] != "abc123" || labels["component"] != "desire-store" {
			t.Fatalf("labels = %v", labels)
		}
		return
	}
	t.Fatal("build_info not gathered")
}

func TestHandlerServesTheRegistry(t *testing.T) {
	m := New("v1.2.3", "abc123")
	m.ObserveRequest("GET", "/x", 200, "", time.Millisecond)

	recorder := httptest.NewRecorder()
	m.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	for _, want := range []string{
		"hyperfleet_desire_store_build_info{",
		`hyperfleet_desire_store_requests_total{component="desire-store",method="GET",path="/x",status_code="200"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("scrape lacks %q:\n%s", want, body)
		}
	}
}
