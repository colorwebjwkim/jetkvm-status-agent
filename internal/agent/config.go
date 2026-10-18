package agent

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	MQTTListen          string `json:"mqttListen"`
	DeviceClientID      string `json:"deviceClientId"`
	RelayBroker         string `json:"relayBroker"`
	RelayAllowPlaintext bool   `json:"relayAllowPlaintext"`
	HTTPListen          string `json:"httpListen"`
	MQTTBroker          string `json:"mqttBroker"`
	TopicPrefix         string `json:"topicPrefix"`
	StaleAfterSeconds   int    `json:"staleAfterSeconds"`
	TLSCert             string `json:"tlsCert"`
	TLSKey              string `json:"tlsKey"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	f, e := os.Open(path)
	if e != nil {
		return c, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return c, e
	}
	if info.Size() > 65536 {
		return c, errors.New("config exceeds 64 KiB")
	}
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	if d.Decode(new(any)) != io.EOF {
		return c, errors.New("config must contain one JSON object")
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.MQTTListen != "" {
		host, port, err := net.SplitHostPort(c.MQTTListen)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || c.DeviceClientID == "" {
			return errors.New("mqttListen requires numeric loopback and deviceClientId")
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("invalid embedded MQTT port")
		}
	}
	if c.RelayBroker != "" {
		if c.MQTTListen == "" {
			return errors.New("relay requires embedded MQTT input to prevent broker feedback loops")
		}
		u, err := url.Parse(c.RelayBroker)
		if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "tcp" && u.Scheme != "ssl") || u.Path != "" || u.RawQuery != "" {
			return errors.New("invalid relayBroker")
		}
		if u.Scheme == "tcp" && !c.RelayAllowPlaintext {
			return errors.New("plain relay requires explicit relayAllowPlaintext on trusted LAN")
		}
		if c.RelayBroker == c.MQTTBroker {
			return errors.New("relay source and destination must differ")
		}
		if c.MQTTListen != "" && u.Host == c.MQTTListen {
			return errors.New("relay cannot target embedded broker")
		}
		if c.MQTTListen != "" {
			_, port, _ := net.SplitHostPort(c.MQTTListen)
			destIP := net.ParseIP(u.Hostname())
			destPort := u.Port()
			if destPort == "" {
				if u.Scheme == "ssl" {
					destPort = "8883"
				} else {
					destPort = "1883"
				}
			}
			if port == destPort && (u.Hostname() == "localhost" || destIP != nil && destIP.IsLoopback()) {
				return errors.New("relay loopback loop rejected")
			}
		}
	}
	host, port, e := net.SplitHostPort(c.HTTPListen)
	if e != nil || port == "" {
		return errors.New("httpListen must be host:port")
	}
	portNumber, e := strconv.Atoi(port)
	if e != nil || portNumber < 1 || portNumber > 65535 {
		return errors.New("invalid HTTP port")
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return errors.New("both tlsCert and tlsKey are required")
	}
	ip := net.ParseIP(host)
	if (ip == nil || !ip.IsLoopback()) && c.TLSCert == "" {
		return errors.New("non-loopback HTTP requires TLS")
	}
	if c.MQTTListen == "" {
		u, e := url.Parse(c.MQTTBroker)
		if e != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "tcp" && u.Scheme != "ssl") {
			return errors.New("mqttBroker must be tcp:// or ssl:// without credentials")
		}
		ip = net.ParseIP(u.Hostname())
		if u.Scheme == "tcp" && (ip == nil || !ip.IsLoopback()) {
			return errors.New("remote MQTT requires ssl://")
		}
	}
	if c.TopicPrefix == "" || strings.ContainsAny(c.TopicPrefix, "+#\x00 ") || strings.HasSuffix(c.TopicPrefix, "/") {
		return errors.New("invalid exact device topicPrefix")
	}
	if c.StaleAfterSeconds < 1 || c.StaleAfterSeconds > 86400 {
		return errors.New("staleAfterSeconds must be 1..86400")
	}
	return nil
}
