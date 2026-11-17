// Package auth authenticates Desire Store API requests from the Wristband that
// the gateway issues.
//
// The x-wristband header is the only credential the service accepts. Every
// caller attribute that authorization uses (caller class, identity, and
// partition) comes from the validated Wristband claims, never from another
// request header.
package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/util/validation"
)

// HeaderWristband is the request header that carries the Wristband.
const HeaderWristband = "X-Wristband"

// Wristband claims set by the gateway. The partition claim name is
// configurable; see Options.PartitionClaim.
const (
	ClaimCallerClass = "hf_caller_class"
	ClaimIdentity    = "hf_identity"
)

// Key-set refresh policy, the same as the HyperFleet API's (HYPERFLEET-1756).
// The periodic refresh drops retired keys. A Wristband signed with a key
// identifier the service has not seen triggers an extra refresh, so a rotated
// key works at once. A new key cannot be told from a junk one before the
// refresh, so the extra refreshes share one budget: a burst of
// unknownKIDRefreshBurst, refilled at one per unknownKIDRefreshEvery. A
// request waits at most unknownKIDWaitMax for a refresh slot and the refresh
// itself; when the next slot is further away it fails at once with 401.
const (
	jwksRefreshInterval    = time.Hour
	unknownKIDRefreshEvery = 30 * time.Second
	unknownKIDRefreshBurst = 3
	unknownKIDWaitMax      = 5 * time.Second
	jwksHTTPTimeout        = 30 * time.Second
	clockLeeway            = 30 * time.Second
)

// Authentication error codes, from the Error Model standard.
const (
	CodeMissing   = "HYPERFLEET-AUT-001"
	CodeInvalid   = "HYPERFLEET-AUT-002"
	CodeExpired   = "HYPERFLEET-AUT-003"
	CodeMalformed = "HYPERFLEET-AUT-004"
)

// CallerClass is the hf_caller_class claim.
type CallerClass string

// Caller classes the gateway issues.
const (
	ClassRemoteApplier CallerClass = "remote-applier"
	ClassFleetScoped   CallerClass = "fleet-scoped"
)

// Caller is the authenticated caller, taken from validated Wristband claims.
type Caller struct {
	Class     CallerClass
	Identity  string
	Partition string
}

type callerKey struct{}

// CallerFromContext returns the caller that the middleware authenticated.
func CallerFromContext(ctx context.Context) (Caller, bool) {
	caller, ok := ctx.Value(callerKey{}).(Caller)
	return caller, ok
}

// Error is an authentication failure. Code is the Error Model code; Err holds
// the cause for logs and is never sent to the client.
type Error struct {
	Code   string
	Detail string
	Err    error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return e.Code + ": " + e.Detail
	}
	return e.Code + ": " + e.Detail + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

var errMissingKID = errors.New("the Wristband has no kid header")

// Validator validates Wristbands against the trusted key set.
type Validator struct {
	keys           keyfunc.Keyfunc
	parser         *jwt.Parser
	partitionClaim string
	cancel         context.CancelFunc
	// lookups lets concurrent lookups of one key share one call, so the first
	// burst of Wristbands signed with a rotated key causes one fetch.
	lookups singleflight.Group
}

// NewValidator loads the trusted key set and builds the Wristband parser. It
// fails when the key set cannot be fetched or holds no key, so a process that
// cannot verify Wristbands never serves. Close stops the background key
// refresh.
func NewValidator(ctx context.Context, opts Options) (*Validator, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	client, err := jwksClient(opts.JWKSCAFile)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	failFirstFetch := false
	keys, err := keyfunc.NewDefaultOverrideCtx(ctx, []string{opts.JWKSURL}, keyfunc.Override{
		Client:                    client,
		RefreshInterval:           jwksRefreshInterval,
		RefreshUnknownKID:         rate.NewLimiter(rate.Every(unknownKIDRefreshEvery), unknownKIDRefreshBurst),
		RateLimitWaitMax:          unknownKIDWaitMax,
		NoErrorReturnFirstHTTPReq: &failFirstFetch,
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("loading the Wristband key set from %s: %w", opts.JWKSURL, err)
	}
	if loaded, err := keys.Storage().KeyReadAll(ctx); err != nil || len(loaded) == 0 {
		cancel()
		return nil, fmt.Errorf("the Wristband key set at %s holds no key", opts.JWKSURL)
	}

	return &Validator{
		keys: keys,
		parser: jwt.NewParser(
			jwt.WithValidMethods(opts.Algorithms),
			jwt.WithIssuer(opts.Issuer),
			jwt.WithAudience(opts.Audience),
			jwt.WithExpirationRequired(),
			jwt.WithLeeway(clockLeeway),
		),
		partitionClaim: opts.PartitionClaim,
		cancel:         cancel,
	}, nil
}

// Close stops the background key-set refresh.
func (v *Validator) Close() {
	v.cancel()
}

// Middleware authenticates every request before next runs. A request that
// fails authentication goes to reject, and next never sees it.
func (v *Validator) Middleware(next http.Handler,
	reject func(http.ResponseWriter, *http.Request, *Error),
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, err := v.Authenticate(r)
		if err != nil {
			reject(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, caller)))
	})
}

// Authenticate validates the request's Wristband and returns its caller. It
// reads no header other than x-wristband, which carries the bare JWT that
// Authorino issues, with no auth scheme.
func (v *Validator) Authenticate(r *http.Request) (Caller, *Error) {
	raw := strings.TrimSpace(r.Header.Get(HeaderWristband))
	if raw == "" {
		return Caller{}, &Error{Code: CodeMissing, Detail: "The x-wristband header is missing."}
	}

	claims := jwt.MapClaims{}
	if _, err := v.parser.ParseWithClaims(raw, claims, v.keyForToken(r.Context())); err != nil {
		switch {
		case errors.Is(err, jwt.ErrTokenExpired):
			return Caller{}, &Error{Code: CodeExpired, Detail: "The Wristband has expired.", Err: err}
		case errors.Is(err, jwt.ErrTokenMalformed):
			return Caller{}, &Error{Code: CodeMalformed, Detail: "The Wristband is malformed.", Err: err}
		default:
			return Caller{}, &Error{Code: CodeInvalid, Detail: "The Wristband is invalid.", Err: err}
		}
	}

	caller, err := v.callerFromClaims(claims)
	if err != nil {
		return Caller{}, &Error{Code: CodeInvalid, Detail: "The Wristband claims are invalid.", Err: err}
	}
	return caller, nil
}

// keyForToken requires a key identifier before the key-set lookup. Without
// one, keyfunc would try every trusted key instead of the identified one.
// Concurrent lookups of the same key identifier and algorithm share one
// lookup. The key-set library gives every caller that misses the cache its
// own refresh and fails the ones that find the refresh budget empty, even
// when the key is about to arrive.
//
// The shared lookup does not stop when the request that started it is
// canceled, because the other requests still wait for its result. The
// key-set library bounds it with unknownKIDWaitMax, and the HTTP client with
// jwksHTTPTimeout. Each request stops waiting when its own context ends.
func (v *Validator) keyForToken(ctx context.Context) jwt.Keyfunc {
	return func(token *jwt.Token) (any, error) {
		kid, ok := token.Header["kid"].(string)
		if !ok || kid == "" {
			return nil, errMissingKID
		}
		alg, ok := token.Header["alg"].(string)
		if !ok {
			return v.keys.KeyfuncCtx(ctx)(token) // the library rejects it with its own error
		}
		shared := v.keys.KeyfuncCtx(context.WithoutCancel(ctx))
		lookup := v.lookups.DoChan(kid+"\x00"+alg, func() (any, error) { return shared(token) })
		select {
		case result := <-lookup:
			return result.Val, result.Err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (v *Validator) callerFromClaims(claims jwt.MapClaims) (Caller, error) {
	class, ok := claims[ClaimCallerClass].(string)
	if !ok || (CallerClass(class) != ClassRemoteApplier && CallerClass(class) != ClassFleetScoped) {
		return Caller{}, fmt.Errorf("claim %s must be %q or %q", ClaimCallerClass, ClassRemoteApplier, ClassFleetScoped)
	}
	identity, ok := claims[ClaimIdentity].(string)
	if !ok || strings.TrimSpace(identity) == "" {
		return Caller{}, fmt.Errorf("claim %s must be a non-empty string", ClaimIdentity)
	}
	partition, ok := claims[v.partitionClaim].(string)
	if !ok {
		return Caller{}, fmt.Errorf("claim %s must be a string", v.partitionClaim)
	}
	if errs := validation.IsDNS1123Label(partition); len(errs) > 0 {
		return Caller{}, fmt.Errorf("claim %s is not a DNS-1123 label: %s", v.partitionClaim, strings.Join(errs, "; "))
	}
	return Caller{Class: CallerClass(class), Identity: identity, Partition: partition}, nil
}

func jwksClient(caFile string) (*http.Client, error) {
	if caFile == "" {
		return &http.Client{Timeout: jwksHTTPTimeout}, nil
	}
	pem, err := os.ReadFile(filepath.Clean(caFile))
	if err != nil {
		return nil, fmt.Errorf("reading wristband-jwks-ca-file %q: %w", caFile, err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("loading the system trust store for wristband-jwks-ca-file: %w", err)
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("wristband-jwks-ca-file %q holds no PEM certificate", caFile)
	}
	return &http.Client{
		Timeout: jwksHTTPTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}, nil
}
