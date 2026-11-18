package auth

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// Options holds the settings that Wristband validation depends on.
type Options struct {
	// Issuer and Audience are the iss and aud values every Wristband must
	// carry.
	Issuer   string
	Audience string
	// JWKSURL is the key set that signs Wristbands, and JWKSCAFile
	// optionally adds a CA to trust when fetching it.
	JWKSURL    string
	JWKSCAFile string
	// Algorithms lists the accepted signing algorithms.
	Algorithms []string
	// PartitionClaim names the Wristband claim that carries the caller's
	// authorized partition.
	PartitionClaim string
}

// Validate rejects options that cannot validate Wristbands. None of the
// settings can be switched off: a missing value stops the service before it
// serves a request. Errors name the matching service setting.
func (o Options) Validate() error {
	required := []struct{ name, value string }{
		{"partition-claim", o.PartitionClaim},
		{"wristband-issuer", o.Issuer},
		{"wristband-audience", o.Audience},
		{"wristband-jwks-url", o.JWKSURL},
	}
	for _, setting := range required {
		if strings.TrimSpace(setting.value) == "" {
			return fmt.Errorf("%s is required", setting.name)
		}
	}
	// The URL is not echoed in these errors: a malformed one may hold
	// credentials, and startup errors end up in logs.
	jwksURL, err := url.Parse(o.JWKSURL)
	if err != nil {
		return fmt.Errorf("invalid wristband-jwks-url: it does not parse as a URL")
	}
	if (jwksURL.Scheme != "https" && jwksURL.Scheme != "http") || jwksURL.Host == "" {
		return fmt.Errorf("invalid wristband-jwks-url: must be an absolute http or https URL")
	}
	if jwksURL.User != nil {
		return fmt.Errorf("invalid wristband-jwks-url: must not carry user information")
	}
	if len(o.Algorithms) == 0 {
		return fmt.Errorf("wristband-algorithms is required")
	}
	for _, algorithm := range o.Algorithms {
		if strings.TrimSpace(algorithm) == "" {
			return fmt.Errorf("wristband-algorithms must not contain an empty value")
		}
		if jwt.GetSigningMethod(algorithm) == nil || algorithm == jwt.SigningMethodNone.Alg() {
			return fmt.Errorf("unsupported wristband-algorithms value %q", algorithm)
		}
	}
	return nil
}
