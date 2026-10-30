// Package server owns HTTP listeners and their lifecycle.
package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// Config contains the process listener ports. An empty internal port leaves
// the application's internal routes unserved.
type Config struct {
	ExternalPort string
	InternalPort string
}

// Handlers are composed by each application; routes remain service-owned.
type Handlers struct {
	Public   http.Handler
	Internal http.Handler
}

func LoadConfig(getenv func(string) string) (Config, error) {
	c := Config{
		ExternalPort: strings.TrimSpace(getenv("PORT")),
		InternalPort: strings.TrimSpace(getenv("INTERNAL_PORT")),
	}
	if c.ExternalPort == "" {
		c.ExternalPort = "8080"
	}
	if err := validatePort("PORT", c.ExternalPort); err != nil {
		return Config{}, err
	}
	if c.InternalPort != "" {
		if err := validatePort("INTERNAL_PORT", c.InternalPort); err != nil {
			return Config{}, err
		}
		if c.ExternalPort == c.InternalPort {
			return Config{}, errors.New("PORT and INTERNAL_PORT must differ")
		}
	}
	return c, nil
}

func validatePort(name, value string) error {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%s must be between 1 and 65535", name)
	}
	return nil
}

func (c Config) ExternalAddress() string {
	return net.JoinHostPort("", c.ExternalPort)
}

func (c Config) InternalAddress() string {
	return net.JoinHostPort("", c.InternalPort)
}

// Servers binds the application's public and internal handlers to their
// respective listeners. If INTERNAL_PORT is unset, only the public listener
// is started.
func (c Config) Servers(handlers Handlers) ([]*http.Server, error) {
	if handlers.Public == nil {
		return nil, errors.New("public handler is required")
	}
	if handlers.Internal == nil && c.InternalPort != "" {
		return nil, errors.New("internal handler is required when INTERNAL_PORT is set")
	}
	servers := []*http.Server{New(c.ExternalAddress(), handlers.Public)}
	if c.InternalPort != "" {
		servers = append(servers, New(c.InternalAddress(), handlers.Internal))
	}
	return servers, nil
}
