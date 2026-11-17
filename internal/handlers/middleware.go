package handlers

import (
	"log/slog"
	"net/http"

	hfl "github.com/openshift-hyperfleet/hyperfleet-logger"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/auth"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/errors"
)

// traceContext reads the W3C traceparent header and puts its trace ID on the
// request context, where the logger and problem bodies pick it up.
func traceContext(next http.Handler) http.Handler {
	propagator := propagation.TraceContext{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		if spanContext := trace.SpanContextFromContext(ctx); spanContext.HasTraceID() {
			ctx = hfl.WithTraceID(ctx, spanContext.TraceID().String())
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// authenticate runs the Wristband validator before next and answers a
// rejected Wristband with 401. The rejection cause is logged at debug level;
// the Wristband itself is never logged.
func authenticate(validator *auth.Validator, log *slog.Logger, next http.Handler) http.Handler {
	return validator.Middleware(next, func(w http.ResponseWriter, r *http.Request, err *auth.Error) {
		log.DebugContext(r.Context(), "Wristband rejected", "code", err.Code, "error", err.Err)
		handleError(w, r, log, errors.Unauthenticated(err.Code, err.Detail, err.Code == auth.CodeMissing))
	})
}

// notFoundProblems answers a request that matches no route with an Error
// Model problem instead of ServeMux's plain-text 404. A request that matches
// a route with another method keeps ServeMux's 405 and its Allow header, as
// in the HyperFleet API: the Error Model defines no problem for a 405.
//
// It is not a catch-all "/" route: ServeMux falls back to a less specific
// pattern when only the method differs, which would turn every 405 into a
// 404. Instead mux.Handler reports whether any route matched, and only an
// unmatched request has its 404 rewritten.
func notFoundProblems(mux *http.ServeMux, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := mux.Handler(r); pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(&notFoundWriter{ResponseWriter: w, r: r, log: log}, r)
	})
}

// notFoundWriter turns ServeMux's 404 into a problem and drops its
// plain-text body. Any other status passes through unchanged.
type notFoundWriter struct {
	http.ResponseWriter
	r        *http.Request
	log      *slog.Logger
	replaced bool
}

func (w *notFoundWriter) WriteHeader(status int) {
	if status != http.StatusNotFound {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.replaced = true
	handleError(w.ResponseWriter, w.r, w.log, errors.EndpointNotFound())
}

func (w *notFoundWriter) Write(b []byte) (int, error) {
	if w.replaced {
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}
