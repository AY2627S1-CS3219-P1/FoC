package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

type gatewaySecret struct {
	namespace string
	name      string
}

type gatewayManifest struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec struct {
		Listeners []struct {
			Protocol string `json:"protocol"`
			TLS      *struct {
				CertificateRefs []struct {
					Group     string `json:"group"`
					Kind      string `json:"kind"`
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"certificateRefs"`
			} `json:"tls"`
		} `json:"listeners"`
	} `json:"spec"`
	Items []json.RawMessage `json:"items"`
}

// gatewayTLSSecrets follows the rendered listener contract instead of requiring
// a certificate when an upstream HTTPS proxy terminates TLS outside the cluster.
func gatewayTLSSecrets(rendered string) ([]gatewaySecret, error) {
	decoder := json.NewDecoder(strings.NewReader(rendered))
	var manifests []gatewayManifest
	for {
		var manifest gatewayManifest
		if err := decoder.Decode(&manifest); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode gateway manifests: %w", err)
		}
		manifests = append(manifests, manifest)
	}
	var secrets []gatewaySecret
	found := false
	for index := 0; index < len(manifests); index++ {
		manifest := manifests[index]
		if manifest.Kind == "List" {
			for _, item := range manifest.Items {
				var child gatewayManifest
				if err := json.Unmarshal(item, &child); err != nil {
					return nil, fmt.Errorf("decode gateway list item: %w", err)
				}
				manifests = append(manifests, child)
			}
			continue
		}
		if manifest.Kind != "Gateway" || manifest.Metadata.Name != "foc-public" {
			continue
		}
		if found || manifest.Metadata.Namespace != "istio-ingress" || len(manifest.Spec.Listeners) == 0 {
			return nil, errors.New("expected one foc-public Gateway with listeners in istio-ingress")
		}
		found = true
		for _, listener := range manifest.Spec.Listeners {
			if listener.Protocol == "HTTP" && listener.TLS == nil {
				continue
			}
			if listener.Protocol != "HTTPS" || listener.TLS == nil || len(listener.TLS.CertificateRefs) == 0 {
				return nil, errors.New("gateway listeners must use HTTP or HTTPS with certificate Secrets")
			}
			for _, reference := range listener.TLS.CertificateRefs {
				if reference.Group != "" || (reference.Kind != "" && reference.Kind != "Secret") ||
					reference.Name == "" {
					return nil, errors.New("gateway certificate reference must name a core Secret")
				}
				namespace := reference.Namespace
				if namespace == "" {
					namespace = manifest.Metadata.Namespace
				}
				secrets = append(secrets, gatewaySecret{namespace: namespace, name: reference.Name})
			}
		}
	}
	if !found {
		return nil, errors.New("rendered gateway stage has no foc-public Gateway")
	}
	return secrets, nil
}

func (o *operator) checkGatewayTLSSecrets() error {
	rendered, err := o.query(
		"create", "--dry-run=client", "--validate=false", "-k", filepath.Join(o.overlay, "gateway"), "-o", "json",
	)
	if err != nil {
		return err
	}
	secrets, err := gatewayTLSSecrets(rendered)
	if err != nil {
		return err
	}
	for _, secret := range secrets {
		if _, err := o.query("-n", secret.namespace, "get", "secret", secret.name); err != nil {
			return err
		}
	}
	return nil
}
