// Package authn issues and verifies access tokens, and carries the caller's
// identity across process boundaries.
//
// Tokens are Ed25519, and the asymmetry is the point: only the user service
// holds a private key and can mint one, while every other service holds a
// public key and can only check. A shared HMAC secret would give every
// service the ability to forge a token, so a break-in anywhere would be a
// break-in everywhere.
//
// Verification is local. Asking the user service about every request would
// make it a hard dependency of the whole system — when it went down, nothing
// would serve, which is exactly what splitting services was supposed to avoid.
package authn

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/config"
)

// Identity is who the caller is, as far as any service is concerned.
type Identity struct {
	UserID string
	Email  string
	Name   string
	Roles  []string
}

func (i Identity) HasRole(role string) bool {
	for _, r := range i.Roles {
		if r == role {
			return true
		}
	}
	return false
}

type Claims struct {
	jwt.RegisteredClaims
	Email string   `json:"email"`
	Name  string   `json:"name"`
	Roles []string `json:"roles,omitempty"`
}

// Signer mints tokens. Only the user service constructs one.
type Signer struct {
	key       ed25519.PrivateKey
	keyID     string
	issuer    string
	accessTTL time.Duration
}

func NewSigner(cfg config.Auth) (*Signer, error) {
	key, err := resolvePrivateKey(cfg.JWTPrivateKey)
	if err != nil {
		return nil, err
	}
	return &Signer{
		key:       key,
		keyID:     KeyID(key.Public().(ed25519.PublicKey)),
		issuer:    cfg.Issuer,
		accessTTL: cfg.AccessTokenTTL,
	}, nil
}

// Issue mints an access token.
func (s *Signer) Issue(id Identity) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   id.UserID,
			Issuer:    s.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.accessTTL)),
		},
		Email: id.Email,
		Name:  id.Name,
		Roles: id.Roles,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	// The key id rides in the header so a verifier holding two keys during a
	// rotation knows which one to try.
	token.Header["kid"] = s.keyID

	signed, err := token.SignedString(s.key)
	if err != nil {
		return "", fmt.Errorf("authn: sign token: %w", err)
	}
	return signed, nil
}

// Verifier checks tokens. Every service constructs one; it cannot mint.
type Verifier struct {
	keys   map[string]ed25519.PublicKey
	issuer string
}

func NewVerifier(cfg config.Auth) (*Verifier, error) {
	keys, err := resolvePublicKeys(cfg.JWTPublicKeys)
	if err != nil {
		return nil, err
	}
	return &Verifier{keys: keys, issuer: cfg.Issuer}, nil
}

func (v *Verifier) Parse(token string) (Identity, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		// Pinning the algorithm is what stops alg=none, and — now that the key
		// material is public — the more relevant attack of handing back an HMAC
		// token signed with the public key everyone can read.
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}

		if kid, ok := t.Header["kid"].(string); ok {
			if key, found := v.keys[kid]; found {
				return key, nil
			}
			return nil, fmt.Errorf("unknown key id %q", kid)
		}

		// No kid: only workable while exactly one key is configured.
		if len(v.keys) == 1 {
			for _, key := range v.keys {
				return key, nil
			}
		}
		return nil, fmt.Errorf("token has no key id and more than one key is configured")
	}, jwt.WithIssuer(v.issuer), jwt.WithExpirationRequired())
	if err != nil {
		return Identity{}, apperr.Unauthorized("The access token is invalid or expired").WithCause(err)
	}

	return Identity{
		UserID: claims.Subject,
		Email:  claims.Email,
		Name:   claims.Name,
		Roles:  claims.Roles,
	}, nil
}

type contextKey struct{}

func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(contextKey{}).(Identity)
	return id, ok
}

// Require returns the caller's identity or an Unauthorized error.
func Require(ctx context.Context) (Identity, error) {
	id, ok := FromContext(ctx)
	if !ok || id.UserID == "" {
		return Identity{}, apperr.Unauthorized("Authentication is required")
	}
	return id, nil
}
