package agent

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type RelayStatus struct {
	Enabled         bool       `json:"enabled"`
	Connected       bool       `json:"connected"`
	LastError       string     `json:"lastError"`
	LastForwardedAt *time.Time `json:"lastForwardedAt"`
	Forwarded       uint64     `json:"forwarded"`
}

// A single worker serializes forwarding. No persistent session, backlog or offline queue.
type Relay struct {
	cfg    Config
	store  *Store
	wake   chan struct{}
	done   chan struct{}
	once   sync.Once
	cancel context.CancelFunc
}

func StartRelay(cfg Config, store *Store) *Relay {
	ctx, cancel := context.WithCancel(context.Background())
	r := &Relay{cfg: cfg, store: store, wake: make(chan struct{}, 1), done: make(chan struct{}), cancel: cancel}
	store.mu.Lock()
	store.relay.Enabled = cfg.RelayBroker != ""
	store.mu.Unlock()
	go r.run(ctx)
	return r
}
func (r *Relay) Notify() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}
func (r *Relay) Close() { r.once.Do(r.cancel); <-r.done }
func (r *Relay) status(connected bool, code string) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	r.store.relay.Connected = connected
	r.store.relay.LastError = code
}
func (r *Relay) run(ctx context.Context) {
	defer close(r.done)
	if r.cfg.RelayBroker == "" {
		return
	}
	var client mqtt.Client
	var last time.Time
	validBefore := false
	var nextConnect time.Time
	drop := func() {
		if client != nil {
			client.Disconnect(0)
			client = nil
		}
		last = time.Time{}
		validBefore = false
		r.status(false, "relay_disconnected")
	}
	publish := func(suffix string, payload any) bool {
		b, _ := json.Marshal(payload)
		t := client.Publish(r.cfg.TopicPrefix+suffix, 1, false, b)
		if !t.WaitTimeout(3*time.Second) || t.Error() != nil {
			drop()
			return false
		}
		now := time.Now().UTC()
		r.store.mu.Lock()
		r.store.relay.LastForwardedAt = &now
		r.store.relay.Forwarded++
		r.store.mu.Unlock()
		return true
	}
	defer func() {
		if client != nil {
			publish("/status", map[string]bool{"online": false})
			if client != nil {
				client.Disconnect(100)
			}
		}
		r.status(false, "")
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-r.wake:
		}
		if client != nil && !client.IsConnectionOpen() {
			drop()
		}
		if client == nil {
			if time.Now().Before(nextConnect) {
				continue
			}
			nextConnect = time.Now().Add(5 * time.Second)
			hash := sha256.Sum256([]byte(r.cfg.TopicPrefix))
			opts := mqtt.NewClientOptions().AddBroker(r.cfg.RelayBroker).SetClientID(fmt.Sprintf("status-relay-%x", hash[:6])).SetCleanSession(true).SetAutoReconnect(false).SetConnectRetry(false).SetConnectTimeout(3 * time.Second).SetWriteTimeout(3 * time.Second).SetKeepAlive(10 * time.Second).SetPingTimeout(3 * time.Second)
			opts.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}).SetUsername(os.Getenv("JETKVM_RELAY_USERNAME")).SetPassword(os.Getenv("JETKVM_RELAY_PASSWORD"))
			opts.SetWill(r.cfg.TopicPrefix+"/status", `{"online":false}`, 1, false)
			client = mqtt.NewClient(opts)
			t := client.Connect()
			if !t.WaitTimeout(4*time.Second) || t.Error() != nil {
				drop()
				continue
			}
			r.status(true, "")
		}
		count, received, valid := r.store.RelayInput(time.Now())
		if !valid {
			if validBefore || last.IsZero() {
				if !publish("/status", map[string]bool{"online": false}) {
					continue
				}
				last = time.Now()
			}
			validBefore = false
			continue
		}
		if !validBefore || !received.Equal(last) {
			if !publish("/status", map[string]bool{"online": true}) {
				continue
			}
			// Recheck after waiting for the destination: stale data must not be replayed.
			count, received, valid = r.store.RelayInput(time.Now())
			if !valid {
				if publish("/status", map[string]bool{"online": false}) {
					validBefore = false
					last = time.Now()
				}
				continue
			}
			if !publish("/sessions/state", map[string]int{"active_sessions": count}) {
				continue
			}
			last = received
			validBefore = true
		}
	}
}
