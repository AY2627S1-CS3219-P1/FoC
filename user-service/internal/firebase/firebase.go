// Package firebase provides initialization for Firebase Admin SDK.
package firebase

import (
	"context"
	"fmt"
	"os"

	firebase "firebase.google.com/go/v4"
	"github.com/pkg/errors"
)

func InitFirebase() (*firebase.App, error) {
	creds := os.Getenv("FIREBASE_CREDENTIALS_JSON")

	if creds != "" {
		fmt.Println("Using Firebase credentials from environment variable")
		if err := os.WriteFile("/tmp/firebase-creds.json", []byte(creds), 0o600); err != nil {
			return nil, errors.Wrap(err, "Error writing Firebase credentials to temporary file")
		}
		if err := os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/tmp/firebase-creds.json"); err != nil {
			return nil, errors.Wrap(err, "error setting GOOGLE_APPLICATION_CREDENTIALS environment variable")
		}
	} else {
		fmt.Println("No Firebase credentials found in environment variable, using default credentials")
	}

	app, err := firebase.NewApp(context.Background(), nil)
	if err != nil {
		return nil, errors.Wrap(err, "Error initializing Firebase app")
	}

	return app, nil
}
