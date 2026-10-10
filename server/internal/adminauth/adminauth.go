// Package adminauth signs and verifies admin API requests with Ed25519 (docs/operations.md,
// "Admin tool"). The private key stays on the operator's machine; the server holds only the
// public key, so reading the server's environment doesn't let anyone act as admin.
//
// A request is signed over its method, host, request URI, a timestamp, a random nonce and
// a hash of its body. The server rejects signatures older than MaxSkew, and nonces it has
// seen, so a captured request can't be replayed or altered.
package adminauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	HeaderTimestamp = "X-Admin-Timestamp"
	HeaderNonce     = "X-Admin-Nonce"
	HeaderSignature = "X-Admin-Signature"

	// MaxSkew is how far a request's timestamp may be from the server's clock.
	MaxSkew = 60 * time.Second

	domain = "paperperps-admin-v1"
)

var (
	ErrMissing   = errors.New("missing admin signature headers")
	ErrExpired   = errors.New("admin request timestamp outside the allowed window")
	ErrSignature = errors.New("bad admin signature")
)

// GenerateKey returns a new key pair, encoded for storage: the private key's seed and the
// public key, both standard base64.
func GenerateKey() (private, public string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(priv.Seed()), base64.StdEncoding.EncodeToString(pub), nil
}

// ParsePrivateKey decodes a private key from GenerateKey.
func ParsePrivateKey(s string) (ed25519.PrivateKey, error) {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("admin private key must be the base64 of a 32-byte Ed25519 seed")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// ParsePublicKey decodes a public key from GenerateKey.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("admin public key must be the base64 of a 32-byte Ed25519 public key")
	}
	return ed25519.PublicKey(key), nil
}

// PublicKeyOf returns the encoded public key for a private key.
func PublicKeyOf(priv ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))
}

func message(method, host, requestURI string, ts int64, nonce string, body []byte) []byte {
	sum := sha256.Sum256(body)
	return []byte(strings.Join([]string{
		domain, method, strings.ToLower(host), requestURI, strconv.FormatInt(ts, 10), nonce, hex.EncodeToString(sum[:]),
	}, "\n"))
}

// Sign adds signature headers to req, whose body is body. Call it last, after the URL and
// method are final.
func Sign(req *http.Request, body []byte, priv ed25519.PrivateKey, now time.Time) error {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	nonce := hex.EncodeToString(buf)
	ts := now.Unix()
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	sig := ed25519.Sign(priv, message(req.Method, host, req.URL.RequestURI(), ts, nonce, body))
	req.Header.Set(HeaderTimestamp, strconv.FormatInt(ts, 10))
	req.Header.Set(HeaderNonce, nonce)
	req.Header.Set(HeaderSignature, base64.StdEncoding.EncodeToString(sig))
	return nil
}

// Verified is a request whose signature checked out. The caller must still reject its
// nonce if it has been seen before, until Expires.
type Verified struct {
	Nonce   string
	Expires time.Time
}

// Verify checks a server-side request's signature. body is the request's full body.
func Verify(r *http.Request, body []byte, pub ed25519.PublicKey, now time.Time) (Verified, error) {
	tsHeader, nonce, sigHeader := r.Header.Get(HeaderTimestamp), r.Header.Get(HeaderNonce), r.Header.Get(HeaderSignature)
	if tsHeader == "" || nonce == "" || sigHeader == "" {
		return Verified{}, ErrMissing
	}
	ts, err := strconv.ParseInt(tsHeader, 10, 64)
	if err != nil {
		return Verified{}, fmt.Errorf("%w: bad timestamp", ErrSignature)
	}
	if len(nonce) != 32 {
		return Verified{}, fmt.Errorf("%w: bad nonce", ErrSignature)
	}
	if _, err := hex.DecodeString(nonce); err != nil {
		return Verified{}, fmt.Errorf("%w: bad nonce", ErrSignature)
	}
	sig, err := base64.StdEncoding.DecodeString(sigHeader)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return Verified{}, fmt.Errorf("%w: malformed", ErrSignature)
	}
	at := time.Unix(ts, 0)
	if d := now.Sub(at); d > MaxSkew || d < -MaxSkew {
		return Verified{}, ErrExpired
	}
	if !ed25519.Verify(pub, message(r.Method, r.Host, r.RequestURI, ts, nonce, body), sig) {
		return Verified{}, ErrSignature
	}
	return Verified{Nonce: nonce, Expires: at.Add(MaxSkew)}, nil
}
