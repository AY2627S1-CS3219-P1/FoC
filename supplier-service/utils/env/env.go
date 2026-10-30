// Package env reads the supplier service's environment configuration.
package env

import (
	"os"
	"strings"
	"sync"
)

type Environment struct {
	DatabaseURL string
}

var getEnvironment = sync.OnceValue(func() Environment {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		panic("DATABASE_URL environment variable is not set")
	}
	return Environment{
		DatabaseURL: databaseURL,
	}
})

// Get returns the process-wide environment configuration.
func Get() Environment {
	return getEnvironment()
}
