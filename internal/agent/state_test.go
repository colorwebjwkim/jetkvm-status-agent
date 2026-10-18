package agent

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func snapshot(s *Store, now time.Time) string {
	b, _ := json.Marshal(s.Snapshot(now))
	return string(b)
}
func TestStateLifecycle(t *testing.T) {
	s := NewStore(45 * time.Second)
	now := time.Now().UTC()
	s.Connection(true)
	if strings.Contains(snapshot(s, now), `"value":false`) {
		t.Fatal("unknown synthesized as off")
	}
	if err := s.Apply("/atx/state", []byte(`{"power":true}`), true, now); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(snapshot(s, now), `"freshness":"fresh"`) {
		t.Fatal("retained must stay unknown")
	}
	if err := s.Apply("/atx/state", []byte(`{"power":false}`), false, now); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot(s, now), `"freshness":"fresh"`) {
		t.Fatal("live state not fresh")
	}
	if !strings.Contains(snapshot(s, now.Add(time.Minute)), `"freshness":"stale"`) {
		t.Fatal("TTL not enforced")
	}
	s.Connection(false)
	if strings.Contains(snapshot(s, now), `"freshness":"fresh"`) {
		t.Fatal("disconnected fresh")
	}
	s.Connection(true)
	if strings.Contains(snapshot(s, now), `"value":false`) {
		t.Fatal("reconnect kept old state")
	}
	for _, payload := range []string{`{}`, `{"power":null}`, `{"power":"true"}`, `bad`} {
		if s.Apply("/atx/state", []byte(payload), false, now) == nil {
			t.Fatal(payload)
		}
	}
	if s.Apply("/sessions/state", []byte(`{"active_sessions":-1}`), false, now) == nil {
		t.Fatal("negative sessions")
	}
}
func TestAPI(t *testing.T) {
	h := Handler(NewStore(time.Minute), strings.Repeat("x", 32))
	for _, tc := range []struct {
		method, path, auth string
		code               int
	}{{"GET", "/healthz", "", 200}, {"GET", "/api/v1/status", "", 401}, {"GET", "/api/v1/status", "Bearer wrong", 401}, {"GET", "/api/v1/status", "Bearer " + strings.Repeat("x", 32), 200}, {"POST", "/api/v1/status", "", 405}} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r.Header.Set("Authorization", tc.auth)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
	}
}

func TestExplicitOfflineReport(t *testing.T) {
	s := NewStore(time.Minute)
	now := time.Now()
	s.Connection(true)
	s.Apply("/atx/state", []byte(`{"power":true}`), false, now)
	s.Apply("/status", []byte(`{"online":false}`), false, now)
	encoded := snapshot(s, now)
	var result map[string]json.RawMessage
	json.Unmarshal([]byte(encoded), &result)
	var device struct {
		Online Reading `json:"online"`
	}
	json.Unmarshal(result["device"], &device)
	var power struct {
		ATX Reading `json:"atx"`
	}
	json.Unmarshal(result["power"], &power)
	if device.Online.Freshness != "fresh" || power.ATX.Freshness != "stale" {
		t.Fatal(encoded)
	}
}
func TestConfig(t *testing.T) {
	c := Config{HTTPListen: "127.0.0.1:9080", MQTTBroker: "tcp://127.0.0.1:1883", TopicPrefix: "jetkvm/device", StaleAfterSeconds: 45}
	if c.Validate() != nil {
		t.Fatal(c.Validate())
	}
	for _, change := range []func(*Config){func(c *Config) { c.HTTPListen = "0.0.0.0:9080" }, func(c *Config) { c.MQTTBroker = "tcp://example.com:1883" }, func(c *Config) { c.TopicPrefix = "jetkvm/#" }, func(c *Config) { c.StaleAfterSeconds = 0 }} {
		bad := c
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("unsafe config accepted")
		}
	}
}
