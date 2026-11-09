package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
)

// applicationRequiresSMTP follows the rendered User deployment. AWS SES removes
// its SMTP reference; local and existing SMTP overlays keep requiring it.
func applicationRequiresSMTP(rendered string) (bool, error) {
	var manifests struct {
		Items []struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							EnvFrom []struct {
								SecretRef *struct {
									Name     string `json:"name"`
									Optional bool   `json:"optional"`
								} `json:"secretRef"`
							} `json:"envFrom"`
						} `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(rendered), &manifests); err != nil {
		return false, fmt.Errorf("decode application manifests: %w", err)
	}
	for _, manifest := range manifests.Items {
		if manifest.Kind != "Deployment" || manifest.Metadata.Name != "user-service" ||
			manifest.Metadata.Namespace != "foc" {
			continue
		}
		for _, container := range manifest.Spec.Template.Spec.Containers {
			for _, entry := range container.EnvFrom {
				if entry.SecretRef != nil && entry.SecretRef.Name == "smtp" && !entry.SecretRef.Optional {
					return true, nil
				}
			}
		}
		return false, nil
	}
	return false, errors.New("rendered applications have no user-service Deployment in foc")
}

func (o *operator) checkSMTPSecret() error {
	rendered, err := o.query(
		"create",
		"--dry-run=client",
		"--validate=false",
		"-k",
		filepath.Join(o.overlay, "applications"),
		"-o",
		"json",
	)
	if err != nil {
		return err
	}
	required, err := applicationRequiresSMTP(rendered)
	if err != nil {
		return err
	}
	if required {
		_, err = o.query("-n", "foc", "get", "secret", "smtp")
	}
	return err
}
