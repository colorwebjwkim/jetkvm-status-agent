package agent

import (
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	broker "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
)

type deviceHook struct {
	broker.HookBase
	cfg    Config
	store  *Store
	relay  *Relay
	mu     sync.Mutex
	active *broker.Client
}

func (h *deviceHook) ID() string { return "device-status-only" }
func (h *deviceHook) Provides(b byte) bool {
	return b == broker.OnConnectAuthenticate || b == broker.OnACLCheck || b == broker.OnSessionEstablished || b == broker.OnDisconnect || b == broker.OnPublish
}
func (h *deviceHook) OnConnectAuthenticate(cl *broker.Client, pk packets.Packet) bool {
	return cl.ID == h.cfg.DeviceClientID
}
func (h *deviceHook) OnACLCheck(cl *broker.Client, topic string, write bool) bool {
	// JetKVM also publishes discovery/metrics at QoS 1. Rejecting them disconnects MQTT 3.1.1.
	// Acknowledge producer writes, but discard everything outside the exact four input topics.
	return write && cl.ID == h.cfg.DeviceClientID
}
func (h *deviceHook) OnSessionEstablished(cl *broker.Client, pk packets.Packet) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.active = cl
	h.store.Connection(true)
	h.relay.Notify()
}
func (h *deviceHook) OnDisconnect(cl *broker.Client, err error, expire bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cl != h.active {
		return
	}
	h.active = nil
	h.store.Connection(false)
	h.store.Error("device_disconnected")
	h.relay.Notify()
}
func (h *deviceHook) OnPublish(cl *broker.Client, pk packets.Packet) (packets.Packet, error) {
	pk.FixedHeader.Retain = false // no retained store; this endpoint has no subscribers
	pk.Ignore = true
	h.mu.Lock()
	defer h.mu.Unlock()
	if cl != h.active {
		return pk, nil
	}
	allowed := false
	for _, suffix := range []string{"/status", "/atx/state", "/dc/state", "/sessions/state"} {
		if pk.TopicName == h.cfg.TopicPrefix+suffix {
			allowed = true
		}
	}
	if !allowed {
		return pk, nil
	}
	if len(pk.Payload) > 16384 {
		h.store.Reject(strings.TrimPrefix(pk.TopicName, h.cfg.TopicPrefix), "payload_too_large")
		h.relay.Notify()
		return pk, nil
	}
	// PUBLISH.RETAIN here requests storage; it is a live producer packet, not a retained replay.
	if h.store.Apply(strings.TrimPrefix(pk.TopicName, h.cfg.TopicPrefix), pk.Payload, false, time.Now().UTC()) != nil {
		h.store.Reject(strings.TrimPrefix(pk.TopicName, h.cfg.TopicPrefix), "invalid_payload")
		h.relay.Notify()
	} else {
		h.relay.Notify()
	}
	return pk, nil
}
func StartBroker(cfg Config, store *Store, relay *Relay) (*broker.Server, error) {
	caps := broker.NewDefaultServerCapabilities()
	caps.MaximumClients = 2
	caps.MaximumPacketSize = 32768
	caps.MaximumInflight = 16
	caps.MaximumSessionExpiryInterval = 1
	caps.MaximumMessageExpiryInterval = 60
	srv := broker.New(&broker.Options{Capabilities: caps, Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))})
	if err := srv.AddHook(&deviceHook{cfg: cfg, store: store, relay: relay}, nil); err != nil {
		return nil, err
	}
	if err := srv.AddListener(listeners.NewTCP(listeners.Config{ID: "loopback", Address: cfg.MQTTListen})); err != nil {
		return nil, err
	}
	if err := srv.Serve(); err != nil {
		srv.Close()
		return nil, err
	}
	return srv, nil
}
