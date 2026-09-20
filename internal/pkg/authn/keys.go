package authn

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// devSeed produces the keypair every service falls back to when no key is
// configured. Fixed rather than generated: the whole point is that the user
// service and every verifier agree without anyone configuring anything
// locally. A per-process random key would mean a token minted by one service
// is rejected by the next.
var devSeed = []byte("wuzz-development-signing-seed!!!")

// DevKeyPair returns the development keypair. config.Load refuses to start
// without real keys outside dev, so this is never reachable there.
func DevKeyPair() (ed25519.PrivateKey, ed25519.PublicKey) {
	private := ed25519.NewKeyFromSeed(devSeed)
	return private, private.Public().(ed25519.PublicKey)
}

// KeyID is a short fingerprint of a public key, derived rather than
// configured so the signer and every verifier compute the same `kid` from the
// key material alone.
func KeyID(public ed25519.PublicKey) string {
	sum := sha256.Sum256(public)
	return hex.EncodeToString(sum[:8])
}

func resolvePrivateKey(configured string) (ed25519.PrivateKey, error) {
	if strings.TrimSpace(configured) == "" {
		private, _ := DevKeyPair()
		return private, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(configured))
	if err != nil {
		return nil, fmt.Errorf("authn: JWT_PRIVATE_KEY is not valid base64: %w", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("authn: JWT_PRIVATE_KEY is %d bytes, want %d", len(raw), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(raw), nil
}

func resolvePublicKeys(configured string) (map[string]ed25519.PublicKey, error) {
	if strings.TrimSpace(configured) == "" {
		_, public := DevKeyPair()
		return map[string]ed25519.PublicKey{KeyID(public): public}, nil
	}

	keys := make(map[string]ed25519.PublicKey)
	for _, part := range strings.Split(configured, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(part)
		if err != nil {
			return nil, fmt.Errorf("authn: JWT_PUBLIC_KEYS entry is not valid base64: %w", err)
		}
		if len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("authn: JWT_PUBLIC_KEYS entry is %d bytes, want %d", len(raw), ed25519.PublicKeySize)
		}
		key := ed25519.PublicKey(raw)
		keys[KeyID(key)] = key
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("authn: no public keys")
	}
	return keys, nil
}
