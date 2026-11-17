// Package authtest mints Wristbands for tests and serves the JWKS that
// verifies them.
package authtest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/golang-jwt/jwt/v5"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/auth"
)

// Values a Signer puts in every Wristband unless a test overrides them.
const (
	Issuer         = "https://authorino.test/desire-store-fleet-scoped/x-wristband"
	Audience       = "desire-store-api"
	PartitionClaim = "hf_partition"
	SweeperID      = "system:serviceaccount:hyperfleet:desire-sweeper"
)

// Signer holds ES256 signing keys and serves the public half of the
// published ones as a JWKS from an httptest server.
type Signer struct {
	server    *httptest.Server
	mu        sync.Mutex
	keys      map[string]*ecdsa.PrivateKey
	published []string
	current   string
	next      int
	requests  atomic.Int64
	failing   atomic.Bool
}

// NewSigner starts a JWKS server that publishes one signing key.
func NewSigner(t testing.TB) *Signer {
	t.Helper()
	s := &Signer{keys: map[string]*ecdsa.PrivateKey{}}
	s.current = s.addKey(t)
	s.published = []string{s.current}
	s.server = httptest.NewServer(http.HandlerFunc(s.serveJWKS))
	t.Cleanup(s.server.Close)
	return s
}

// Options returns Wristband validation options that trust this signer.
func (s *Signer) Options() auth.Options {
	return auth.Options{
		Issuer:         Issuer,
		Audience:       Audience,
		JWKSURL:        s.URL(),
		Algorithms:     []string{"ES256"},
		PartitionClaim: PartitionClaim,
	}
}

// URL is the JWKS endpoint.
func (s *Signer) URL() string { return s.server.URL }

// Requests counts the JWKS requests served so far, failed ones included.
func (s *Signer) Requests() int64 { return s.requests.Load() }

// SetFailing makes the JWKS endpoint answer 500 while failing is true.
func (s *Signer) SetFailing(failing bool) { s.failing.Store(failing) }

// KID is the key identifier of the current signing key.
func (s *Signer) KID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

// Rotate adds a new signing key, publishes it next to the existing ones, and
// makes it current. It returns the new key identifier.
func (s *Signer) Rotate(t testing.TB) string {
	t.Helper()
	kid := s.addKey(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.published = append(s.published, kid)
	s.current = kid
	return kid
}

// Claims returns a valid claim set for one caller, expiring in five minutes.
func Claims(class, identity, partition string) jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"iss":                 Issuer,
		"aud":                 Audience,
		"iat":                 now.Unix(),
		"exp":                 now.Add(5 * time.Minute).Unix(),
		auth.ClaimCallerClass: class,
		auth.ClaimIdentity:    identity,
		PartitionClaim:        partition,
	}
}

// Sign signs claims with the current key and sets its kid.
func (s *Signer) Sign(t testing.TB, claims jwt.MapClaims) string {
	t.Helper()
	kid := s.KID()
	return s.SignWithKey(t, claims, s.key(t, kid), kid)
}

// SignAs signs claims with the key that kid names and sets that kid.
func (s *Signer) SignAs(t testing.TB, claims jwt.MapClaims, kid string) string {
	t.Helper()
	return s.SignWithKey(t, claims, s.key(t, kid), kid)
}

// SignWithKey signs claims with any key and sets kid, or no kid header when
// kid is empty.
func (s *Signer) SignWithKey(t testing.TB, claims jwt.MapClaims, key *ecdsa.PrivateKey, kid string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("signing a Wristband: %v", err)
	}
	return signed
}

// CurrentKey returns the current private signing key.
func (s *Signer) CurrentKey(t testing.TB) *ecdsa.PrivateKey {
	t.Helper()
	return s.key(t, s.KID())
}

// NewUntrustedKey returns a key that no JWKS publishes.
func NewUntrustedKey(t testing.TB) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating an ECDSA key: %v", err)
	}
	return key
}

func (s *Signer) key(t testing.TB, kid string) *ecdsa.PrivateKey {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.keys[kid]
	if !ok {
		t.Fatalf("no signing key %q", kid)
	}
	return key
}

func (s *Signer) addKey(t testing.TB) string {
	t.Helper()
	key := NewUntrustedKey(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	kid := fmt.Sprintf("wristband-key-%d", s.next)
	s.keys[kid] = key
	return kid
}

func (s *Signer) serveJWKS(w http.ResponseWriter, r *http.Request) {
	s.requests.Add(1)
	if s.failing.Load() {
		http.Error(w, "key set unavailable", http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	set := jwkset.JWKSMarshal{}
	for _, kid := range s.published {
		jwk, err := jwkset.NewJWKFromKey(&s.keys[kid].PublicKey, jwkset.JWKOptions{
			Metadata: jwkset.JWKMetadataOptions{KID: kid, ALG: jwkset.AlgES256, USE: jwkset.UseSig},
		})
		if err != nil {
			s.mu.Unlock()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		set.Keys = append(set.Keys, jwk.Marshal())
	}
	s.mu.Unlock()

	body, err := json.Marshal(set)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
}
