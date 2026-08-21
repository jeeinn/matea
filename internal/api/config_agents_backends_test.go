package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jeeinn/matea/internal/config"
)

// memStore is an in-memory config.Store for handler tests.
type memStore struct{ data map[string]string }

func newMemStore() *memStore { return &memStore{data: map[string]string{}} }
func (m *memStore) GetConfig(key string) (string, error) { return m.data[key], nil }
func (m *memStore) SetConfig(key, value string) error    { m.data[key] = value; return nil }
func (m *memStore) DeleteConfig(key string) error        { delete(m.data, key); return nil }
func (m *memStore) ListConfigs() (map[string]string, error) {
	out := map[string]string{}
	for k, v := range m.data {
		out[k] = v
	}
	return out, nil
}

// TestUpdateAgentsBackendsKeepCurrentOnMask verifies a PUT carrying the
// password mask placeholder preserves the previously stored real password
// (mirrors llm.providers / C-17) and survives a simulated restart.
func TestUpdateAgentsBackendsKeepCurrentOnMask(t *testing.T) {
	store := newMemStore()
	cm := config.NewConfigManager(&config.Config{})
	cm.SetStore(store)
	seed := `{"default":"oc1","backends":{"oc1":{"type":"hub-opencode","base_url":"http://oc:8081","auth":{"username":"u","password":"REALPASS"}}}}`
	if err := cm.Update("agents.backends", seed); err != nil {
		t.Fatalf("seed update: %v", err)
	}

	h := &Handler{cfgManager: cm}

	// PUT with the password masked (what the UI sends after a GET).
	masked := `{"default":"oc1","backends":{"oc1":{"type":"hub-opencode","base_url":"http://oc:8081","auth":{"username":"u","password":"********"}}}}`
	payload, _ := json.Marshal(map[string]string{"agents.backends": masked})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(payload))
	h.updateConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", rec.Code, rec.Body.String())
	}

	// In-memory config must keep the REAL password.
	if got := cm.Get().Agents.Backends.Backends["oc1"].Auth.Password; got != "REALPASS" {
		t.Fatalf("expected real password preserved, got %q", got)
	}

	// GET response must mask the password.
	disp, err := cm.GetDisplayMap()
	if err != nil {
		t.Fatalf("display map: %v", err)
	}
	raw, _ := disp["agents.backends"].(string)
	if strings.Contains(config.MaskSensitiveInBackendsJSON(raw), "REALPASS") {
		t.Fatalf("GET must not leak password, got %q", raw)
	}

	// Simulate a restart: rebuild manager from the same DB store.
	cm2 := config.NewConfigManager(&config.Config{})
	cm2.SetStore(store)
	if err := cm2.ApplyDBOverrides(); err != nil {
		t.Fatalf("apply overrides restart: %v", err)
	}
	if got := cm2.Get().Agents.Backends.Backends["oc1"].Auth.Password; got != "REALPASS" {
		t.Fatalf("after restart expected REALPASS, got %q (DB stored masked value)", got)
	}
}

// TestRestoreMaskedBackendPasswords verifies the API-layer restore replaces
// the mask placeholder with the active password and passes real values through.
func TestRestoreMaskedBackendPasswords(t *testing.T) {
	cm := config.NewConfigManager(&config.Config{})
	seed := `{"default":"oc1","backends":{"oc1":{"type":"hub-opencode","base_url":"http://oc:8081","auth":{"username":"u","password":"REALPASS"}}}}`
	if err := cm.Update("agents.backends", seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := &Handler{cfgManager: cm}

	masked := `{"default":"oc1","backends":{"oc1":{"type":"hub-opencode","base_url":"http://oc:8081","auth":{"username":"u","password":"********"}}}}`
	restored := h.restoreMaskedBackendPasswords(masked)
	if strings.Contains(restored, "********") {
		t.Fatalf("expected mask replaced, got %s", restored)
	}
	if !strings.Contains(restored, "REALPASS") {
		t.Fatalf("expected REALPASS restored, got %s", restored)
	}

	// Real password should pass through unchanged (re-marshaling may add
	// zero-value fields, so we check semantically rather than byte-for-byte).
	real := `{"default":"oc2","backends":{"oc2":{"type":"hub-opencode","base_url":"http://oc:8082","auth":{"username":"u2","password":"fresh"}}}}`
	out := h.restoreMaskedBackendPasswords(real)
	if !strings.Contains(out, "fresh") {
		t.Fatalf("real password should be present, got %s", out)
	}
	if strings.Contains(out, "********") {
		t.Fatalf("real password must not be masked, got %s", out)
	}
}
