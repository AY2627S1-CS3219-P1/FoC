// Package env reads the supplier service's environment configuration.
package env

import (
	"os"
	"strings"
	"sync"
)

type Environment struct {
	Port        string
	DatabaseURL string
}

var getEnvironment = sync.OnceValue(func() Environment {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		panic("DATABASE_URL environment variable is not set")
	}
	return Environment{
		Port:        port,
		DatabaseURL: databaseURL,
	}
})

// Get returns the process-wide environment configuration.
func Get() Environment {
	return getEnvironment()
}
