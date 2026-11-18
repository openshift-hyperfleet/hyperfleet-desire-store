package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/auth"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/auth/authtest"
)

const (
	adapterID   = "system:serviceaccount:hyperfleet:cluster-adapter-hyperfleet-adapter"
	applierID   = "system:serviceaccount:hyperfleet:applier"
	partitionID = "mc-01"
)

type recorder struct {
	calls    int
	caller   auth.Caller
	rejected *auth.Error
}

func (rec *recorder) next() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.calls++
		rec.caller, _ = auth.CallerFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

func (rec *recorder) reject(w http.ResponseWriter, _ *http.Request, err *auth.Error) {
	rec.rejected = err
	w.WriteHeader(http.StatusUnauthorized)
}

func newValidator(t *testing.T, signer *authtest.Signer) *auth.Validator {
	t.Helper()
	v, err := auth.NewValidator(context.Background(), signer.Options())
	if err != nil {
		t.Fatalf("NewValidator() error: %v", err)
	}
	t.Cleanup(v.Close)
	return v
}

func serve(v *auth.Validator, rec *recorder, header http.Header) int {
	return serveContext(context.Background(), v, rec, header)
}

func serveContext(ctx context.Context, v *auth.Validator, rec *recorder, header http.Header) int {
	request := httptest.NewRequestWithContext(ctx, http.MethodGet,
		"/api/hyperfleet/desire-store/v1/partitions/mc-01/desires", nil)
	for name, values := range header {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	response := httptest.NewRecorder()
	v.Middleware(rec.next(), rec.reject).ServeHTTP(response, request)
	return response.Code
}

func wristband(token string) http.Header {
	return http.Header{auth.HeaderWristband: {token}}
}

func TestValidWristbandReachesTheHandler(t *testing.T) {
	signer := authtest.NewSigner(t)
	v := newValidator(t, signer)
	rec := &recorder{}

	token := signer.Sign(t, authtest.Claims(string(auth.ClassFleetScoped), adapterID, partitionID))
	if code := serve(v, rec, wristband(token)); code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (rejection %v)", code, rec.rejected)
	}

	want := auth.Caller{Class: auth.ClassFleetScoped, Identity: adapterID, Partition: partitionID}
	if rec.calls != 1 || rec.caller != want {
		t.Fatalf("next handler calls = %d with caller %+v, want 1 with %+v", rec.calls, rec.caller, want)
	}
}

// TestBearerPrefixIsRejected pins the contract: Authorino sends the bare JWT
// in x-wristband, so a value with an auth scheme is not a Wristband.
func TestBearerPrefixIsRejected(t *testing.T) {
	signer := authtest.NewSigner(t)
	v := newValidator(t, signer)
	rec := &recorder{}

	token := signer.Sign(t, authtest.Claims(string(auth.ClassRemoteApplier), applierID, partitionID))
	if code := serve(v, rec, wristband("Bearer "+token)); code != http.StatusUnauthorized || rec.calls != 0 {
		t.Fatalf("status = %d with %d handler calls, want 401 and 0", code, rec.calls)
	}
}

func TestRequestsWithoutAWristbandAreRejected(t *testing.T) {
	signer := authtest.NewSigner(t)
	v := newValidator(t, signer)
	valid := signer.Sign(t, authtest.Claims(string(auth.ClassFleetScoped), adapterID, partitionID))

	tests := map[string]http.Header{
		"no header":                    {},
		"valid token in Authorization": {"Authorization": {"Bearer " + valid}},
		"forged identity headers":      {"X-Hf-Identity": {adapterID}, "X-Hf-Caller-Class": {"fleet-scoped"}},
	}
	for name, header := range tests {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{}
			if code := serve(v, rec, header); code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", code)
			}
			if rec.calls != 0 {
				t.Fatal("the next handler ran for a request without a Wristband")
			}
			if rec.rejected == nil || rec.rejected.Code != auth.CodeMissing {
				t.Fatalf("rejection = %v, want %s", rec.rejected, auth.CodeMissing)
			}
		})
	}
}

func TestInvalidWristbandsAreRejected(t *testing.T) {
	signer := authtest.NewSigner(t)
	v := newValidator(t, signer)
	trustedKID := signer.KID()
	trustedKey := signer.CurrentKey(t)

	claims := func(mutate func(jwt.MapClaims)) jwt.MapClaims {
		c := authtest.Claims(string(auth.ClassFleetScoped), adapterID, partitionID)
		mutate(c)
		return c
	}
	same := func(jwt.MapClaims) {}

	tests := map[string]struct {
		token string
		code  string
	}{
		"unknown kid": {
			signer.SignWithKey(t, claims(same), trustedKey, "not-published"), auth.CodeInvalid,
		},
		"missing kid": {
			signer.SignWithKey(t, claims(same), trustedKey, ""), auth.CodeInvalid,
		},
		"bad signature": {
			signer.SignWithKey(t, claims(same), authtest.NewUntrustedKey(t), trustedKID), auth.CodeInvalid,
		},
		"expired": {
			signer.Sign(t, claims(func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-2 * time.Minute).Unix() })),
			auth.CodeExpired,
		},
		"no expiry": {
			signer.Sign(t, claims(func(c jwt.MapClaims) { delete(c, "exp") })), auth.CodeInvalid,
		},
		"wrong issuer": {
			signer.Sign(t, claims(func(c jwt.MapClaims) { c["iss"] = "https://other.issuer" })), auth.CodeInvalid,
		},
		"wrong audience": {
			signer.Sign(t, claims(func(c jwt.MapClaims) { c["aud"] = "hyperfleet-api" })), auth.CodeInvalid,
		},
		"unknown caller class": {
			signer.Sign(t, claims(func(c jwt.MapClaims) { c["hf_caller_class"] = "admin" })), auth.CodeInvalid,
		},

		"empty identity": {
			signer.Sign(t, claims(func(c jwt.MapClaims) { c["hf_identity"] = "" })), auth.CodeInvalid,
		},

		"bad partition": {
			signer.Sign(t, claims(func(c jwt.MapClaims) { c[authtest.PartitionClaim] = "MC_01" })), auth.CodeInvalid,
		},
		"missing partition": {
			signer.Sign(t, claims(func(c jwt.MapClaims) { delete(c, authtest.PartitionClaim) })), auth.CodeInvalid,
		},
		"malformed": {"not.a.jwt", auth.CodeMalformed},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{}
			if code := serve(v, rec, wristband(tc.token)); code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", code)
			}
			if rec.calls != 0 {
				t.Fatal("the next handler ran for an invalid Wristband")
			}
			if rec.rejected == nil || rec.rejected.Code != tc.code {
				t.Fatalf("rejection = %v, want %s", rec.rejected, tc.code)
			}
		})
	}
}

func TestRotatedKeyIsTrustedOnFirstUse(t *testing.T) {
	signer := authtest.NewSigner(t)
	v := newValidator(t, signer)
	before := signer.Requests()

	signer.Rotate(t)
	token := signer.Sign(t, authtest.Claims(string(auth.ClassFleetScoped), adapterID, partitionID))

	rec := &recorder{}
	if code := serve(v, rec, wristband(token)); code != http.StatusOK || rec.calls != 1 {
		t.Fatalf("status = %d with %d handler calls, want 200 and 1 (rejection %v)", code, rec.calls, rec.rejected)
	}
	if got := signer.Requests() - before; got != 1 {
		t.Fatalf("JWKS requests for the rotated key = %d, want 1", got)
	}
}

// TestRotatedKeyIsTrustedAfterAnUnknownKIDRefresh shows that a refresh spent
// on a key identifier nobody publishes leaves budget for a rotated key: its
// first Wristband is accepted at once, not after the periodic refresh or a
// refill of the refresh budget.
func TestRotatedKeyIsTrustedAfterAnUnknownKIDRefresh(t *testing.T) {
	signer := authtest.NewSigner(t)
	v := newValidator(t, signer)
	claims := authtest.Claims(string(auth.ClassFleetScoped), adapterID, partitionID)
	before := signer.Requests()

	junk := signer.SignWithKey(t, claims, signer.CurrentKey(t), "not-published")
	rec := &recorder{}
	if code := serve(v, rec, wristband(junk)); code != http.StatusUnauthorized || rec.calls != 0 {
		t.Fatalf("unknown kid: status = %d with %d handler calls, want 401 and 0", code, rec.calls)
	}
	if got := signer.Requests() - before; got != 1 {
		t.Fatalf("JWKS requests for the unknown kid = %d, want 1", got)
	}

	signer.Rotate(t)
	start := time.Now()
	if code := serve(v, rec, wristband(signer.Sign(t, claims))); code != http.StatusOK || rec.calls != 1 {
		t.Fatalf("rotated kid: status = %d with %d handler calls, want 200 and 1 (rejection %v)",
			code, rec.calls, rec.rejected)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the rotated key was accepted after %s, want no wait for the refresh budget", elapsed)
	}
	if got := signer.Requests() - before; got != 2 {
		t.Fatalf("JWKS requests = %d, want 2: one for the unknown kid, one for the rotated key", got)
	}
}

func TestFailedRefreshKeepsTrustedKeys(t *testing.T) {
	signer := authtest.NewSigner(t)
	v := newValidator(t, signer)
	trustedKID := signer.KID()

	signer.SetFailing(true)
	newKID := signer.Rotate(t)
	before := signer.Requests()

	rec := &recorder{}
	unknown := signer.SignAs(t, authtest.Claims(string(auth.ClassFleetScoped), adapterID, partitionID), newKID)
	if code := serve(v, rec, wristband(unknown)); code != http.StatusUnauthorized || rec.calls != 0 {
		t.Fatalf("new kid during a failed refresh: status = %d with %d handler calls, want 401 and 0",
			code, rec.calls)
	}
	if signer.Requests() <= before {
		t.Fatal("the unknown kid did not trigger a key-set refresh")
	}

	trusted := signer.SignAs(t, authtest.Claims(string(auth.ClassFleetScoped), adapterID, partitionID), trustedKID)
	if code := serve(v, rec, wristband(trusted)); code != http.StatusOK || rec.calls != 1 {
		t.Fatalf("trusted kid after a failed refresh: status = %d with %d handler calls, want 200 and 1 (%v)",
			code, rec.calls, rec.rejected)
	}
}

func TestNewValidatorFailsWithoutAKeySet(t *testing.T) {
	signer := authtest.NewSigner(t)
	opts := signer.Options()

	unreachable := httptest.NewServer(http.NotFoundHandler())
	opts.JWKSURL = unreachable.URL
	unreachable.Close()

	if v, err := auth.NewValidator(context.Background(), opts); err == nil {
		v.Close()
		t.Fatal("NewValidator() succeeded with an unreachable JWKS")
	}

	signer.SetFailing(true)
	opts.JWKSURL = signer.URL()
	if v, err := auth.NewValidator(context.Background(), opts); err == nil {
		v.Close()
		t.Fatal("NewValidator() succeeded with a failing JWKS")
	}
}

func TestNewValidatorFailsOnAnEmptyKeySet(t *testing.T) {
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[]}`))
	}))
	t.Cleanup(empty.Close)
	opts := authtest.NewSigner(t).Options()
	opts.JWKSURL = empty.URL

	if v, err := auth.NewValidator(context.Background(), opts); err == nil {
		v.Close()
		t.Fatal("NewValidator() succeeded with a key set that holds no keys")
	}
}

// TestCanceledRequestLeavesTheSharedLookupRunning cancels the request that
// started a shared key lookup while another request waits for it. The waiting
// request must still pass.
func TestCanceledRequestLeavesTheSharedLookupRunning(t *testing.T) {
	signer := authtest.NewSigner(t)
	target, err := url.Parse(signer.URL())
	if err != nil {
		t.Fatalf("parsing the signer URL: %v", err)
	}
	// The key set is served through a gate that holds responses while
	// holding is set, so the shared lookup stays in flight.
	proxy := httputil.NewSingleHostReverseProxy(target)
	held := make(chan struct{})
	var holding atomic.Bool
	var fetches atomic.Int64
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		if holding.Load() {
			select {
			case <-held:
			case <-r.Context().Done():
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(jwks.Close)
	release := sync.OnceFunc(func() { close(held) })
	t.Cleanup(release)

	opts := signer.Options()
	opts.JWKSURL = jwks.URL
	v, err := auth.NewValidator(context.Background(), opts)
	if err != nil {
		t.Fatalf("NewValidator() error: %v", err)
	}
	t.Cleanup(v.Close)
	signer.Rotate(t)
	token := signer.Sign(t, authtest.Claims(string(auth.ClassFleetScoped), adapterID, partitionID))
	holding.Store(true)
	before := fetches.Load()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := make(chan int, 1)
	go func() { first <- serveContext(ctx, v, &recorder{}, wristband(token)) }()
	deadline := time.Now().Add(5 * time.Second)
	for fetches.Load() == before {
		if time.Now().After(deadline) {
			t.Fatal("the first request did not start a key-set fetch within 5s")
		}
		time.Sleep(time.Millisecond)
	}

	second := make(chan int, 1)
	go func() { second <- serve(v, &recorder{}, wristband(token)) }()
	// Let the second request join the lookup that is in flight.
	time.Sleep(100 * time.Millisecond)
	cancel()
	if code := <-first; code != http.StatusUnauthorized {
		t.Fatalf("canceled request status = %d, want 401", code)
	}

	release()
	if code := <-second; code != http.StatusOK {
		t.Fatalf("waiting request status = %d, want 200 after the canceled request left the lookup", code)
	}
	if got := fetches.Load() - before; got != 1 {
		t.Fatalf("JWKS requests = %d, want 1 shared fetch", got)
	}
}

// TestRotatedKeyBurstSharesOneRefresh sends the first requests signed with a
// rotated key all at once. They must share one key-set fetch and all pass
// without waiting for the refresh budget.
func TestRotatedKeyBurstSharesOneRefresh(t *testing.T) {
	signer := authtest.NewSigner(t)
	v := newValidator(t, signer)
	signer.Rotate(t)
	token := signer.Sign(t, authtest.Claims(string(auth.ClassFleetScoped), adapterID, partitionID))
	before := signer.Requests()

	const burst = 10
	codes := make(chan int, burst)
	start := time.Now()
	var wg sync.WaitGroup
	for range burst {
		wg.Go(func() { codes <- serve(v, &recorder{}, wristband(token)) })
	}
	wg.Wait()
	close(codes)

	for code := range codes {
		if code != http.StatusOK {
			t.Fatalf("a request signed with the rotated key got %d, want 200", code)
		}
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the burst took %s, want no wait for the refresh budget", elapsed)
	}
	if got := signer.Requests() - before; got != 1 {
		t.Fatalf("JWKS requests for the burst = %d, want 1 shared fetch", got)
	}
}
