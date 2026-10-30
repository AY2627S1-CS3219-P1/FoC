// Package server owns HTTP listeners, lifecycle and executable health probes.
package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// Config contains the process ports. An empty internal port means the service
// exposes its complete route set on the external listener.
type Config struct {
	ExternalPort string
	InternalPort string
}

// Handlers are composed by the application, which owns the public/private
// meaning of its routes. All is used when no internal listener is configured.
type Handlers struct {
	Public   http.Handler
	Internal http.Handler
	All      http.Handler
}

func LoadConfig(getenv func(string) string) (Config, error) {
	c := Config{
		ExternalPort: strings.TrimSpace(getenv("EXTERNAL_PORT")),
		InternalPort: strings.TrimSpace(getenv("INTERNAL_PORT")),
	}
	if c.ExternalPort == "" {
		c.ExternalPort = "8080"
	}
	if err := validatePort("EXTERNAL_PORT", c.ExternalPort); err != nil {
		return Config{}, err
	}
	if c.InternalPort != "" {
		if err := validatePort("INTERNAL_PORT", c.InternalPort); err != nil {
			return Config{}, err
		}
		if c.ExternalPort == c.InternalPort {
			return Config{}, errors.New("EXTERNAL_PORT and INTERNAL_PORT must differ")
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

func (c Config) ExternalProbeURL() string {
	return "http://" + net.JoinHostPort("127.0.0.1", c.ExternalPort)
}

func (c Config) InternalProbeURL() string {
	return "http://" + net.JoinHostPort("127.0.0.1", c.InternalPort)
}

// Servers binds public routes to the external listener. A configured internal
// listener gets the application's internal handler; without one, all routes
// are served on the external listener.
func (c Config) Servers(handlers Handlers) ([]*http.Server, error) {
	if handlers.Public == nil {
		return nil, errors.New("public handler is required")
	}
	if c.InternalPort == "" {
		if handlers.All == nil {
			return nil, errors.New("all-routes handler is required when INTERNAL_PORT is unset")
		}
		return []*http.Server{New(c.ExternalAddress(), WithLiveness(handlers.All))}, nil
	}

	if handlers.Internal == nil {
		return nil, errors.New("internal handler is required when INTERNAL_PORT is set")
	}
	return []*http.Server{
		New(c.ExternalAddress(), WithLiveness(handlers.Public)),
		New(c.InternalAddress(), WithLiveness(handlers.Internal)),
	}, nil
}
