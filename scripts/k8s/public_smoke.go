package main

import (
	"errors"
	"net/url"
	"os"
	"strings"
)

// publicSmokePrivatePaths are internal-only RPCs that must return 404 through
// the public edge.
var publicSmokePrivatePaths = []string{
	"/user.v1.PublicKeyService/GetPublicKeys",
	"/credit.v1.CreditReservationService/Reserve",
	"/user.v1.UserInternalService/GetUsers",
}

// publicSmoke checks an existing HTTPS edge without creating mesh probe pods,
// forwarding a gateway, reading local certificates, or mutating business data.
func (o *operator) publicSmoke() error {
	origin := strings.TrimSuffix(os.Getenv("K8S_PUBLIC_ORIGIN"), "/")
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("K8S_PUBLIC_ORIGIN must be an HTTPS origin without credentials, path, query, or fragment")
	}
	if _, err := o.smokeRequest(origin, "", "/", 200, false); err != nil {
		return err
	}
	for _, path := range publicHealthPaths {
		body, err := o.smokeRequest(origin, "", path, 200, true)
		if err != nil {
			return err
		}
		if err := assertHealthResponse(body, path); err != nil {
			return err
		}
	}
	for _, path := range publicSmokePrivatePaths {
		if _, err := o.smokeRequest(origin, "", path, 404, true); err != nil {
			return err
		}
	}
	note("Public HTTPS smoke passed: frontend, public health RPCs, and private RPC routes")
	return nil
}
