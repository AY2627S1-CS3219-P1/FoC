// Package firebase provides initialization for Firebase Admin SDK.
package firebase

import (
	"context"
	"fmt"

	firebase "firebase.google.com/go/v4"
	"github.com/pkg/errors"
	"google.golang.org/api/option"
)

func InitFirebase(credentialsJSON string) (*firebase.App, error) {
	var options []option.ClientOption
	if credentialsJSON != "" {
		fmt.Println("Using Firebase credentials from environment variable")
		options = append(options, option.WithCredentialsJSON([]byte(credentialsJSON)))
	} else {
		fmt.Println("No Firebase credentials found in environment variable, using default credentials")
	}

	app, err := firebase.NewApp(context.Background(), nil, options...)
	if err != nil {
		return nil, errors.Wrap(err, "Error initializing Firebase app")
	}

	return app, nil
}
