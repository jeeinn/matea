package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log"
	"sync"
	"time"
)

// SetupTokenTTL is how long a setup token stays valid (C-2 security model).
// On expiry a NEW token is generated and printed to the console/log — the
// console remains the root of trust, and a running service self-heals without
// a restart.
const SetupTokenTTL = 30 * time.Minute

// SetupTokenManager owns the one-time setup token printed to the console on
// first run. The token proves the operator has console access (i.e. owns the
// deployment) and gates the unauthenticated /api/setup/* endpoints while
// initial configuration is incomplete.
//
// In-memory only: a restart regenerates and reprints the token, which is fine
// because console access is the trust anchor anyway. The token is decoupled
// from the default admin password — knowing one grants nothing about the
// other.
type SetupTokenManager struct {
	mu        sync.Mutex
	token     string
	expiresAt time.Time
	// now is injectable for tests.
	now func() time.Time
	// announce is called with each freshly generated token (defaults to
	// log.Printf). Injectable for tests.
	announce func(format string, args ...interface{})
}

// NewSetupTokenManager creates a manager and generates the first token.
func NewSetupTokenManager() *SetupTokenManager {
	m := &SetupTokenManager{
		now:      time.Now,
		announce: log.Printf,
	}
	m.regenerateLocked()
	return m
}

// Token returns the current token, regenerating (and re-announcing) first if
// it has expired. Empty string means setup is disabled (token invalidated).
func (m *SetupTokenManager) Token() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token == "" {
		return ""
	}
	if m.now().After(m.expiresAt) {
		m.regenerateLocked()
	}
	return m.token
}

// Validate reports whether candidate is the active token. Constant-time
// compare; an expired token triggers regeneration and fails validation.
func (m *SetupTokenManager) Validate(candidate string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token == "" || candidate == "" {
		return false
	}
	if m.now().After(m.expiresAt) {
		m.regenerateLocked()
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(m.token)) == 1
}

// Invalidate permanently disables the token (called when setup completes).
// Subsequent Validate calls fail and Token returns "".
func (m *SetupTokenManager) Invalidate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token = ""
	m.expiresAt = time.Time{}
}

// ExpiresAt exposes the current expiry for the status endpoint / tests.
func (m *SetupTokenManager) ExpiresAt() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.expiresAt
}

func (m *SetupTokenManager) regenerateLocked() {
	buf := make([]byte, 24) // 48 hex chars — copy-friendly, plenty of entropy
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failure is fatal-worthy elsewhere; here fall back to
		// keeping setup disabled rather than issuing a weak token.
		m.token = ""
		m.expiresAt = time.Time{}
		m.announce("[ERROR] Failed to generate setup token: %v — setup endpoints disabled", err)
		return
	}
	m.token = hex.EncodeToString(buf)
	m.expiresAt = m.now().Add(SetupTokenTTL)
	m.announce("\n"+
		"╔══════════════════════════════════════════════════════════════════╗\n"+
		"║  Matea 首次配置向导 / First-run Setup                            ║\n"+
		"╠══════════════════════════════════════════════════════════════════╣\n"+
		"║  打开 Web UI 完成初始化（Gitea + LLM），Setup Token：            ║\n"+
		"║  Open the Web UI to finish setup. Your Setup Token:              ║\n"+
		"║                                                                  ║\n"+
		"║     %s       ║\n"+
		"║                                                                  ║\n"+
		"║  有效期 %s，过期后自动重新生成并打印。                           ║\n"+
		"║  Valid for %s; a new one is printed here on expiry.              ║\n"+
		"╚══════════════════════════════════════════════════════════════════╝",
		m.token, SetupTokenTTL, SetupTokenTTL)
}

// setupTokenFromRequest extracts the setup token from the X-Setup-Token
// header or a Bearer Authorization header.
func setupTokenFromRequest(authHeader, xSetupToken string) string {
	if xSetupToken != "" {
		return xSetupToken
	}
	const prefix = "Bearer "
	if len(authHeader) > len(prefix) && authHeader[:len(prefix)] == prefix {
		return authHeader[len(prefix):]
	}
	return ""
}
