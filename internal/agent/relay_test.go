package agent

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/eclipse/paho.mqtt.golang/packets"
)

func TestRelayPolicy(t *testing.T) {
	s := NewStore(time.Second)
	now := time.Now()
	s.Connection(true)
	s.Apply("/status", []byte(`{"online":true}`), false, now)
	s.Apply("/sessions/state", []byte(`{"active_sessions":0}`), true, now)
	if _, _, ok := s.RelayInput(now); ok {
		t.Fatal("retained zero relayed")
	}
	s.Apply("/sessions/state", []byte(`{"active_sessions":2}`), false, now)
	if n, _, ok := s.RelayInput(now); !ok || n != 2 {
		t.Fatal("live sessions missing")
	}
	if _, _, ok := s.RelayInput(now.Add(2 * time.Second)); ok {
		t.Fatal("stale replay")
	}
	s.Reject("/sessions/state", "invalid_payload")
	s.Apply("/dc/state", []byte(`{"isOn":true}`), false, now)
	if _, _, ok := s.RelayInput(now); ok {
		t.Fatal("invalid sessions revived by unrelated telemetry")
	}
	s.Connection(false)
	if _, _, ok := s.RelayInput(now); ok {
		t.Fatal("offline replay")
	}
}

func TestEmbeddedBrokerRelay(t *testing.T) {
	// Destination deliberately implements publish-only PC receiver, with no SUBSCRIBE support.
	dest, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	events := make(chan string, 32)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			conn, err := dest.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(15 * time.Second))
				pk, err := packets.ReadPacket(conn)
				if err != nil || pk.Details().Qos != 0 {
					return
				}
				(&packets.ConnackPacket{FixedHeader: packets.FixedHeader{MessageType: packets.Connack}, ReturnCode: 0}).Write(conn)
				for {
					pk, err = packets.ReadPacket(conn)
					if err != nil {
						return
					}
					switch v := pk.(type) {
					case *packets.PublishPacket:
						if v.Retain {
							events <- "bad-retain"
						}
						events <- v.TopicName + ":" + string(v.Payload)
						if v.Qos == 1 {
							(&packets.PubackPacket{FixedHeader: packets.FixedHeader{MessageType: packets.Puback}, MessageID: v.MessageID}).Write(conn)
						}
					case *packets.SubscribePacket:
						events <- "bad-subscribe"
						return
					case *packets.PingreqPacket:
						(&packets.PingrespPacket{FixedHeader: packets.FixedHeader{MessageType: packets.Pingresp}}).Write(conn)
					case *packets.DisconnectPacket:
						return
					}
					select {
					case <-ctx.Done():
						return
					default:
					}
				}
			}()
		}
	}()
	port, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := port.Addr().String()
	port.Close()
	cfg := Config{MQTTListen: address, DeviceClientID: "jetkvm-test", TopicPrefix: "jetkvm/test", RelayBroker: "tcp://" + dest.Addr().String(), StaleAfterSeconds: 1}
	store := NewStore(time.Second)
	relay := StartRelay(cfg, store)
	defer relay.Close()
	local, err := StartBroker(cfg, store, relay)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	opts := mqtt.NewClientOptions().AddBroker("tcp://" + address).SetClientID("jetkvm-test").SetCleanSession(true)
	device := mqtt.NewClient(opts)
	token := device.Connect()
	if !token.WaitTimeout(3*time.Second) || token.Error() != nil {
		t.Fatal("device connection failed")
	}
	defer device.Disconnect(0)
	sub := device.Subscribe("jetkvm/test/commands/#", 1, nil)
	if !sub.WaitTimeout(3 * time.Second) {
		t.Fatal("subscription timeout")
	}
	if result := sub.(*mqtt.SubscribeToken).Result()["jetkvm/test/commands/#"]; result != 0x80 {
		t.Fatal("command subscription accepted")
	}
	send := func(suffix string, payload any) {
		b, _ := json.Marshal(payload)
		token := device.Publish(cfg.TopicPrefix+suffix, 1, true, b)
		if !token.WaitTimeout(3*time.Second) || token.Error() != nil {
			t.Fatal("publish failed")
		}
	}
	await := func(want string) {
		deadline := time.After(7 * time.Second)
		for {
			select {
			case got := <-events:
				if got == "bad-retain" || got == "bad-subscribe" {
					t.Fatal(got)
				}
				if got == want {
					return
				}
			case <-deadline:
				t.Fatal("missing " + want)
			}
		}
	}
	send("/status", map[string]bool{"online": true})
	send("/system/state", map[string]int{"memory_used": 1234}) // ignored telemetry must still get PUBACK
	wrong := device.Publish("another-device/sessions/state", 1, true, `{"active_sessions":99}`)
	wrong.WaitTimeout(3 * time.Second)
	if wrong.Error() != nil {
		t.Fatal(wrong.Error())
	}
	send("/sessions/state", map[string]int{"active_sessions": 2})
	await(`jetkvm/test/sessions/state:{"active_sessions":2}`)
	await(`jetkvm/test/status:{"online":false}`) // TTL expiry signals unknown, never zero.
	// Wrong device and exact-prefix mismatch cannot inject observations.
	bad := mqtt.NewClient(mqtt.NewClientOptions().AddBroker("tcp://" + address).SetClientID("wrong-device"))
	tok := bad.Connect()
	tok.WaitTimeout(3 * time.Second)
	if tok.Error() == nil {
		bad.Disconnect(0)
		t.Fatal("wrong client accepted")
	}
	send("/status", map[string]bool{"online": true})
	send("/sessions/state", map[string]int{"active_sessions": 0})
	await(`jetkvm/test/sessions/state:{"active_sessions":0}`)
	device.Disconnect(100)
	await(`jetkvm/test/status:{"online":false}`)
}

func TestRelayConfigValidation(t *testing.T) {
	c := Config{HTTPListen: "127.0.0.1:9080", MQTTListen: "127.0.0.1:1883", DeviceClientID: "jetkvm-test", TopicPrefix: "jetkvm/test", RelayBroker: "tcp://pc.example:1883", RelayAllowPlaintext: true, StaleAfterSeconds: 45}
	if c.Validate() != nil {
		t.Fatal(c.Validate())
	}
	for _, change := range []func(*Config){func(c *Config) { c.MQTTListen = "" }, func(c *Config) { c.MQTTListen = "0.0.0.0:1883" }, func(c *Config) { c.RelayAllowPlaintext = false }, func(c *Config) { c.RelayBroker = "tcp://localhost:1883" }, func(c *Config) { c.RelayBroker = "tcp://127.0.0.2:1883" }} {
		bad := c
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("unsafe relay config accepted")
		}
	}
}
