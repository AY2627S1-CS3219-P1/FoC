// Package env reads the supplier service's environment configuration.
package env

import (
	"os"
	"strings"
	"sync"
)

type Environment struct {
	Port         string
	InternalPort string
	DatabaseURL  string
}

var getEnvironment = sync.OnceValue(func() Environment {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}
	// Every service runs the listener split: private routes will move to the
	// internal listener, so it is bound even before any route is split.
	internalPort := strings.TrimSpace(os.Getenv("INTERNAL_PORT"))
	if internalPort == "" {
		panic("INTERNAL_PORT environment variable is not set")
	}
	if internalPort == port {
		panic("INTERNAL_PORT must differ from PORT")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		panic("DATABASE_URL environment variable is not set")
	}
	return Environment{
		Port:         port,
		InternalPort: internalPort,
		DatabaseURL:  databaseURL,
	}
})

// Get returns the process-wide environment configuration.
func Get() Environment {
	return getEnvironment()
}
