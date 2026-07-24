package integrations

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

const cursorLifetime = 24 * time.Hour

type cursorClaims struct {
	After       string `json:"after"`
	ExpiresUnix int64  `json:"exp"`
	WorkspaceID string `json:"wid"`
}

type CursorCodec struct {
	key   []byte
	clock Clock
}

func NewCursorCodec(key []byte, clock Clock) (*CursorCodec, error) {
	if len(key) < 32 {
		return nil, errors.New("cursor signing key must contain at least 32 bytes")
	}
	if clock == nil {
		clock = systemClock
	}
	return &CursorCodec{key: append([]byte(nil), key...), clock: clock}, nil
}

func (codec *CursorCodec) Encode(workspaceID, after string) (string, error) {
	if workspaceID == "" || after == "" {
		return "", ErrInvalidArgument
	}
	claims, err := json.Marshal(cursorClaims{
		After:       after,
		ExpiresUnix: codec.clock().Add(cursorLifetime).Unix(),
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return "", err
	}
	signature := codec.sign(claims)
	return base64.RawURLEncoding.EncodeToString(claims) + "." +
		base64.RawURLEncoding.EncodeToString(signature), nil
}

func (codec *CursorCodec) Decode(workspaceID, encoded string) (string, error) {
	separator := -1
	for index := range encoded {
		if encoded[index] == '.' {
			if separator != -1 {
				return "", ErrInvalidCursor
			}
			separator = index
		}
	}
	if separator <= 0 || separator == len(encoded)-1 {
		return "", ErrInvalidCursor
	}
	claimsJSON, err := base64.RawURLEncoding.DecodeString(encoded[:separator])
	if err != nil {
		return "", ErrInvalidCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(encoded[separator+1:])
	if err != nil || !hmac.Equal(signature, codec.sign(claimsJSON)) {
		return "", ErrInvalidCursor
	}
	var claims cursorClaims
	decoderError := json.Unmarshal(claimsJSON, &claims)
	if decoderError != nil ||
		claims.WorkspaceID == "" ||
		claims.WorkspaceID != workspaceID ||
		claims.After == "" ||
		!codec.clock().Before(time.Unix(claims.ExpiresUnix, 0)) {
		return "", ErrInvalidCursor
	}
	return claims.After, nil
}

func (codec *CursorCodec) sign(value []byte) []byte {
	mac := hmac.New(sha256.New, codec.key)
	_, _ = mac.Write(value)
	return mac.Sum(nil)
}
