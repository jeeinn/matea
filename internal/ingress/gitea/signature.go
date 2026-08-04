package gitea

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
)

// VerifySignature validates the X-Gitea-Signature header using HMAC-SHA256.
func VerifySignature(r *http.Request, secret string, body []byte) bool {
	sig := r.Header.Get("X-Gitea-Signature")
	if sig == "" || secret == "" {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(sig), []byte(expected))
}

// ReadBody reads and returns the request body.
func ReadBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	return io.ReadAll(r.Body)
}
