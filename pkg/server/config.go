// Package server owns HTTP transport, listener lifecycle and executable probes.
package server

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
)

type Mode string

const (
	Single Mode = "single"
	Mesh   Mode = "mesh"
)

type Config struct {
	Mode Mode
	Port string
}

// LoadConfig keeps the Compose listener by default. Mesh ports are fixed to
// match the sidecar's two service ports and never bind a pod interface.
func LoadConfig(getenv func(string) string) (Config, error) {
	mode, err := ParseMode(getenv("RPC_MODE"))
	if err != nil {
		return Config{}, err
	}
	c := Config{Mode: mode, Port: strings.TrimSpace(getenv("PORT"))}
	if c.Port == "" {
		c.Port = "8080"
	}
	port, err := strconv.Atoi(c.Port)
	if err != nil || port < 1 || port > 65535 {
		return Config{}, errors.New("PORT must be between 1 and 65535")
	}
	return c, nil
}

func ParseMode(value string) (Mode, error) {
	mode := Mode(strings.TrimSpace(value))
	if mode == "" {
		return Single, nil
	}
	if mode != Single && mode != Mesh {
		return "", errors.New("RPC_MODE must be single or mesh")
	}
	return mode, nil
}

func (c Config) PublicAddress() string {
	if c.Mode == Mesh {
		return "127.0.0.1:8080"
	}
	return net.JoinHostPort("", c.Port)
}

func (c Config) ProbeURL() string {
	if c.Mode == Mesh {
		return "http://127.0.0.1:8080"
	}
	return "http://" + net.JoinHostPort("127.0.0.1", c.Port)
}

// Servers constructs the public listener once; internal routing delegates to
// that handler so authentication and interceptors stay identical on both ports.
func (c Config) Servers(public, internal http.Handler) []*http.Server {
	servers := []*http.Server{New(c.PublicAddress(), WithLiveness(public))}
	if c.Mode == Mesh {
		if internal == nil {
			internal = public
		}
		servers = append(servers, New("127.0.0.1:8081", WithLiveness(internal)))
	}
	return servers
}
