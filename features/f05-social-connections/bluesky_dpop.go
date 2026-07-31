package socialconnections

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type blueskyDPoPKey struct {
	D string `json:"d"`
	X string `json:"x"`
	Y string `json:"y"`
}

func newBlueskyDPoPKey() (blueskyDPoPKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return blueskyDPoPKey{}, err
	}
	return blueskyDPoPKey{
		D: base64.RawURLEncoding.EncodeToString(key.D.Bytes()),
		X: base64.RawURLEncoding.EncodeToString(key.X.Bytes()),
		Y: base64.RawURLEncoding.EncodeToString(key.Y.Bytes()),
	}, nil
}

func (key blueskyDPoPKey) private() (*ecdsa.PrivateKey, error) {
	decode := func(value string) ([]byte, error) {
		return base64.RawURLEncoding.DecodeString(value)
	}
	d, dErr := decode(key.D)
	x, xErr := decode(key.X)
	y, yErr := decode(key.Y)
	if dErr != nil || xErr != nil || yErr != nil {
		return nil, errors.New("invalid DPoP key encoding")
	}
	private := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     new(big.Int).SetBytes(x),
			Y:     new(big.Int).SetBytes(y),
		},
		D: new(big.Int).SetBytes(d),
	}
	if !private.Curve.IsOnCurve(private.X, private.Y) ||
		private.D.Sign() <= 0 {
		return nil, errors.New("invalid DPoP key")
	}
	return private, nil
}

func (key blueskyDPoPKey) proof(
	method string,
	target *url.URL,
	nonce, accessToken string,
	now time.Time,
) (string, error) {
	private, err := key.private()
	if err != nil {
		return "", err
	}
	header := map[string]any{
		"typ": "dpop+jwt",
		"alg": "ES256",
		"jwk": map[string]string{
			"kty": "EC",
			"crv": "P-256",
			"x":   key.X,
			"y":   key.Y,
		},
	}
	jti, err := randomOpaqueID(24)
	if err != nil {
		return "", err
	}
	claims := map[string]any{
		"jti": jti,
		"htm": strings.ToUpper(method),
		"htu": blueskyDPoPHTU(target),
		"iat": now.Unix(),
		"exp": now.Add(time.Minute).Unix(),
	}
	if nonce != "" {
		claims["nonce"] = nonce
	}
	if accessToken != "" {
		hashed := sha256.Sum256([]byte(accessToken))
		claims["ath"] = base64.RawURLEncoding.EncodeToString(hashed[:])
	}
	headerJSON, _ := json.Marshal(header)
	claimsJSON, _ := json.Marshal(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, private, digest[:])
	if err != nil {
		return "", err
	}
	size := (private.Curve.Params().BitSize + 7) / 8
	signature := make([]byte, size*2)
	r.FillBytes(signature[:size])
	s.FillBytes(signature[size:])
	return signingInput + "." +
		base64.RawURLEncoding.EncodeToString(signature), nil
}

func blueskyDPoPHTU(target *url.URL) string {
	clone := *target
	clone.RawQuery = ""
	clone.Fragment = ""
	clone.User = nil
	return clone.String()
}

func blueskyDPoPError(response *http.Response, body []byte) bool {
	if response.StatusCode != http.StatusBadRequest &&
		response.StatusCode != http.StatusUnauthorized {
		return false
	}
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil &&
		payload.Error == "use_dpop_nonce" {
		return true
	}
	return strings.Contains(
		strings.ToLower(response.Header.Get("WWW-Authenticate")),
		`error="use_dpop_nonce"`,
	)
}

func blueskyValidateDPoPProof(proof string) error {
	parts := strings.Split(proof, ".")
	if len(parts) != 3 {
		return fmt.Errorf("invalid DPoP compact JWT")
	}
	for _, part := range parts {
		if _, err := base64.RawURLEncoding.DecodeString(part); err != nil {
			return fmt.Errorf("invalid DPoP compact JWT")
		}
	}
	return nil
}
