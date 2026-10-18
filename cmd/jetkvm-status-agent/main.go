package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/colorwebjwkim/jetkvm-status-agent/internal/agent"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

func main() {
	path := flag.String("config", "config.json", "configuration file")
	version := flag.Bool("version", false, "print version")
	flag.Parse()
	if *version {
		println(agent.Version)
		return
	}
	cfg, err := agent.LoadConfig(*path)
	if err != nil {
		log.Fatal(err)
	}
	token := os.Getenv("JETKVM_AGENT_TOKEN")
	if len(token) < 32 {
		log.Fatal("JETKVM_AGENT_TOKEN must be at least 32 bytes")
	}
	store := agent.NewStore(time.Duration(cfg.StaleAfterSeconds) * time.Second)
	store.Error("mqtt_connecting")
	relay := agent.StartRelay(cfg, store)
	defer relay.Close()
	var client mqtt.Client
	if cfg.MQTTListen != "" {
		localBroker, err := agent.StartBroker(cfg, store, relay)
		if err != nil {
			log.Fatal("local MQTT listener failed: ", err)
		}
		defer localBroker.Close()
	} else {
		deviceHash := sha256.Sum256([]byte(cfg.TopicPrefix))
		opts := mqtt.NewClientOptions().AddBroker(cfg.MQTTBroker).SetClientID(fmt.Sprintf("status-agent-%x", deviceHash[:6])).SetCleanSession(true)
		opts.SetUsername(os.Getenv("JETKVM_MQTT_USERNAME")).SetPassword(os.Getenv("JETKVM_MQTT_PASSWORD"))
		opts.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12})
		opts.SetConnectTimeout(5 * time.Second).SetAutoReconnect(true).SetConnectRetry(true).SetConnectRetryInterval(5 * time.Second)
		opts.SetKeepAlive(20 * time.Second).SetPingTimeout(5 * time.Second)
		opts.SetOnConnectHandler(func(c mqtt.Client) {
			store.Connection(true)
			topics := map[string]byte{}
			for _, suffix := range []string{"/status", "/atx/state", "/dc/state", "/sessions/state"} {
				topics[cfg.TopicPrefix+suffix] = 1
			}
			t := c.SubscribeMultiple(topics, func(_ mqtt.Client, m mqtt.Message) {
				if len(m.Payload()) > 16384 {
					store.Reject(strings.TrimPrefix(m.Topic(), cfg.TopicPrefix), "payload_too_large")
					relay.Notify()
					return
				}
				if store.Apply(strings.TrimPrefix(m.Topic(), cfg.TopicPrefix), m.Payload(), m.Retained(), time.Now().UTC()) != nil {
					store.Reject(strings.TrimPrefix(m.Topic(), cfg.TopicPrefix), "invalid_payload")
				}
				relay.Notify()
			})
			go func() {
				if !t.WaitTimeout(10*time.Second) || t.Error() != nil {
					store.Connection(false)
					store.Error("subscribe_failed")
					return
				}
				if subscription, ok := t.(*mqtt.SubscribeToken); ok {
					for _, result := range subscription.Result() {
						if result == 0x80 {
							store.Connection(false)
							store.Error("subscribe_denied")
							return
						}
					}
				}
			}()
		})
		opts.SetConnectionLostHandler(func(_ mqtt.Client, _ error) {
			store.Connection(false)
			store.Error("mqtt_disconnected")
			relay.Notify()
		})
		client = mqtt.NewClient(opts)
		client.Connect()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{Addr: cfg.HTTPListen, Handler: agent.Handler(store, token), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Print("jetkvm-status-agent ", agent.Version, " starting")
	if cfg.TLSCert != "" {
		err = srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
	} else {
		err = srv.ListenAndServe()
	}
	if client != nil {
		client.Disconnect(250)
	}
	if err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
