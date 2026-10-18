package agent

import (
	"encoding/json"
	"errors"
	"sync"
	"time"
)

const Version = "0.1.0"

type Reading struct {
	Value            any        `json:"value"`
	ReceivedAt       *time.Time `json:"receivedAt"`
	SourceObservedAt *time.Time `json:"sourceObservedAt"`
	Retained         bool       `json:"retained"`
	Freshness        string     `json:"freshness"`
}
type Store struct {
	mu        sync.Mutex
	connected bool
	lastError string
	readings  map[string]Reading
	ttl       time.Duration
}

func NewStore(ttl time.Duration) *Store { return &Store{readings: map[string]Reading{}, ttl: ttl} }
func (s *Store) Connection(connected bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = connected
	if connected {
		s.readings = map[string]Reading{}
		s.lastError = ""
	}
}
func (s *Store) Error(code string) { s.mu.Lock(); defer s.mu.Unlock(); s.lastError = code }
func (s *Store) Apply(suffix string, payload []byte, retained bool, now time.Time) error {
	var value any
	switch suffix {
	case "/status":
		var v struct {
			Online *bool `json:"online"`
		}
		if json.Unmarshal(payload, &v) != nil || v.Online == nil {
			return errors.New("invalid status")
		}
		value = *v.Online
	case "/atx/state":
		var v struct {
			Power *bool `json:"power"`
		}
		if json.Unmarshal(payload, &v) != nil || v.Power == nil {
			return errors.New("invalid ATX state")
		}
		value = *v.Power
	case "/dc/state":
		var v struct {
			On *bool `json:"isOn"`
		}
		if json.Unmarshal(payload, &v) != nil || v.On == nil {
			return errors.New("invalid DC state")
		}
		value = *v.On
	case "/sessions/state":
		var v struct {
			Active *int `json:"active_sessions"`
		}
		if json.Unmarshal(payload, &v) != nil || v.Active == nil || *v.Active < 0 {
			return errors.New("invalid sessions")
		}
		value = *v.Active
	default:
		return errors.New("unsupported topic")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readings[suffix] = Reading{Value: value, ReceivedAt: &now, Retained: retained}
	s.lastError = ""
	return nil
}
func (s *Store) Snapshot(now time.Time) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	get := func(key string) Reading {
		v := s.readings[key]
		v.Freshness = "unknown"
		if v.ReceivedAt != nil && !v.Retained {
			v.Freshness = "stale"
			if s.connected && now.Sub(*v.ReceivedAt) <= s.ttl {
				v.Freshness = "fresh"
			}
		}
		return v
	}
	return map[string]any{
		"schemaVersion": 1,
		"agent":         map[string]any{"version": Version, "collectorConnected": s.connected, "lastError": s.lastError},
		"device":        map[string]any{"online": get("/status"), "extension": "unknown", "extensionVerified": false},
		"power":         map[string]any{"atx": get("/atx/state"), "dc": get("/dc/state"), "usableForControl": false},
		"sessions":      get("/sessions/state"),
	}
}
