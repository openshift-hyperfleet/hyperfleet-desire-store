package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/metrics"
)

const (
	unmatchedPath = "unmatched"
	otherMethod   = "OTHER"
)

// requestOutcome carries what the metrics middleware cannot see from the
// outside: the Error Model code that handleError wrote.
type requestOutcome struct {
	errorCode string
}

type outcomeKey struct{}

func recordErrorCode(ctx context.Context, code string) {
	if outcome, ok := ctx.Value(outcomeKey{}).(*requestOutcome); ok {
		outcome.errorCode = code
	}
}

// statusRecorder remembers the status the handler chain wrote.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// instrument records every request that reaches the data plane, rejected ones
// included, so it must be the outermost handler. The route label comes from
// mux.Handler: inner middlewares pass on a copy of the request, so r.Pattern
// is never set on the request seen here. Unmatched requests, 404 and 405,
// share one label value, and an unknown method is reported as OTHER, so a
// caller cannot create series.
func instrument(m *metrics.Metrics, mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := unmatchedPath
		if _, pattern := mux.Handler(r); pattern != "" {
			if _, route, found := strings.Cut(pattern, " "); found {
				path = route
			} else {
				path = pattern
			}
		}
		method := otherMethod
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
			http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
			method = r.Method
		}

		outcome := &requestOutcome{}
		recorder := &statusRecorder{ResponseWriter: w}
		start := time.Now()
		next.ServeHTTP(recorder, r.WithContext(context.WithValue(r.Context(), outcomeKey{}, outcome)))
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}
		m.ObserveRequest(method, path, recorder.status, outcome.errorCode, time.Since(start))
	})
}
