package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func compactJSON(value any) string {
	// Only JSON-compatible manifest literals and decoded JSON values reach here.
	data, _ := json.Marshal(value)
	return string(data)
}

func manifestPatch(kind, name string, value any) map[string]any {
	target := map[string]any{"kind": kind}
	if name != "" {
		target["name"] = name
	}
	return map[string]any{"target": target, "patch": compactJSON(value)}
}

func certificatePatch(kind, name, container string, ses bool) map[string]any {
	podContainer := map[string]any{
		"name":         container,
		"volumeMounts": []any{map[string]any{"name": "rds-ca", "mountPath": "/var/run/foc/rds", "readOnly": true}},
	}
	if ses {
		podContainer["envFrom"] = []any{
			map[string]any{"configMapRef": map[string]any{"name": "foc-config"}},
			map[string]any{"secretRef": map[string]any{"name": "communication-db"}},
			map[string]any{"secretRef": map[string]any{"name": "telegram"}},
		}
	}
	apiVersion := "apps/v1"
	if kind == "Job" {
		apiVersion = "batch/v1"
	}
	return map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
			"containers": []any{
				podContainer,
			},
			"volumes": []any{map[string]any{"name": "rds-ca", "configMap": map[string]any{"name": "rds-ca"}}},
		}}},
	}
}

func (a *application) saveKustomization(directory string, value map[string]any) error {
	if err := os.Remove(filepath.Join(directory, "kustomization.json")); err != nil && !os.IsNotExist(err) {
		return errors.New("cannot remove obsolete generated kustomization")
	}
	return saveJSON(a.root, filepath.Join(directory, "kustomization.yaml"), value)
}

func (a *application) renderOverlay(config map[string]any, destination, ca string) error {
	for _, key := range []string{"region", "cluster_name", "vpc_id", "public_hostname", "image_prefix", "order_bucket", "order_role_arn", "ingress_role_arn", "gateway_target_arn", "loki_bucket", "loki_role_arn"} {
		if stringValue(config, key) == "" {
			return fmt.Errorf("missing Terraform output: %s", key)
		}
	}
	if config["loki_bucket"] == config["order_bucket"] || config["loki_role_arn"] == config["order_role_arn"] {
		return errors.New("Loki requires its own bucket and workload role")
	}
	for _, key := range []string{"ses_sender_email", "communication_email_role_arn"} {
		if value := config[key]; value != nil {
			if _, ok := value.(string); !ok {
				return errors.New("SES configuration must use strings or null")
			}
		}
	}
	sender, role := stringValue(config, "ses_sender_email"), stringValue(config, "communication_email_role_arn")
	if (sender != "") != (role != "") {
		return errors.New("SES requires both ses_sender_email and communication_email_role_arn")
	}
	if sender != "" && !regexp.MustCompile(`^[^@\s<>]+@[^@\s<>]+\.[^@\s<>]+$`).MatchString(sender) {
		return errors.New("ses_sender_email must be a bare verified email address")
	}
	hostname := stringValue(config, "public_hostname")
	if !regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?\.[a-z]{2,}$`).MatchString(hostname) {
		return errors.New("public_hostname must be a hostname, not a URL")
	}
	if !strings.Contains(ca, "-----BEGIN CERTIFICATE-----") {
		return errors.New("supply the RDS CA PEM bundle from the AWS trust store")
	}
	destination, err := privatePath(a.root, destination)
	if err != nil {
		return err
	}
	stages := []string{"gateway", "dependencies", "provisioning", "observability", "initialization", "applications"}
	for _, stage := range stages {
		directory := filepath.Join(destination, stage)
		base := filepath.Join(a.root, "infra/k8s/base", stage)
		resources := []any{}
		if stage != "provisioning" {
			relative, err := filepath.Rel(directory, base)
			if err != nil {
				return errors.New("cannot resolve Kubernetes base")
			}
			resources = append(resources, relative)
		}
		kustomization := map[string]any{"apiVersion": "kustomize.config.k8s.io/v1beta1", "kind": "Kustomization"}
		patches := []any{}
		switch stage {
		case "gateway":
			service := map[string]any{"spec": map[string]any{"type": "ClusterIP"}}
			patches = append(
				patches,
				manifestPatch(
					"ConfigMap",
					"foc-public-infrastructure",
					[]any{map[string]any{"op": "replace", "path": "/data/service", "value": compactJSON(service)}},
				),
				manifestPatch("Gateway", "foc-public", []any{
					map[string]any{
						"op":    "replace",
						"path":  "/spec/listeners/0/port",
						"value": 80,
					},
					map[string]any{"op": "replace", "path": "/spec/listeners/0/protocol", "value": "HTTP"},
					map[string]any{"op": "remove", "path": "/spec/listeners/0/tls"},
				}),
			)
			binding := map[string]any{
				"apiVersion": "elbv2.k8s.aws/v1beta1",
				"kind":       "TargetGroupBinding",
				"metadata":   map[string]any{"name": "foc-public", "namespace": "istio-ingress"},
				"spec": map[string]any{
					"serviceRef": map[string]any{
						"name": "foc-public-istio",
						"port": 80,
					},
					"targetGroupARN": config["gateway_target_arn"],
					"targetType":     "ip",
					"vpcID":          config["vpc_id"],
				},
			}
			if err := saveJSON(a.root, filepath.Join(directory, "target-group-binding.json"), binding); err != nil {
				return err
			}
			resources = append(resources, "target-group-binding.json")
		case "dependencies":
			values := map[string]any{
				"AWS_REGION":                 config["region"],
				"AWS_STS_REGIONAL_ENDPOINTS": "regional",
				"ORDER_S3_BUCKET":            config["order_bucket"],
				"ORDER_S3_ENDPOINT":          "",
				"ORDER_S3_PUBLIC_ENDPOINT":   "https://s3." + stringValue(config, "region") + ".amazonaws.com",
			}
			for _, key := range []string{"FRONTEND_BASE_URL", "ORIGIN", "PUBLIC_USER_SERVICE_URL", "PUBLIC_SUPPLIER_SERVICE_URL", "PUBLIC_ORDER_SERVICE_URL", "PUBLIC_COMMUNICATION_SERVICE_URL", "PUBLIC_CREDIT_SERVICE_URL"} {
				values[key] = "https://" + hostname
			}
			if sender != "" {
				values["EMAIL_PROVIDER"] = "ses"
				values["EMAIL_FROM"] = sender
				patches = append(
					patches,
					manifestPatch(
						"ServiceAccount",
						"communication-service",
						[]any{
							map[string]any{
								"op":    "add",
								"path":  "/metadata/annotations",
								"value": map[string]any{"eks.amazonaws.com/role-arn": role},
							},
						},
					),
				)
			}
			keys := make([]string, 0, len(values))
			for key := range values {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			operations := []any{}
			for _, key := range keys {
				operations = append(
					operations,
					map[string]any{"op": "add", "path": "/data/" + key, "value": values[key]},
				)
			}
			patches = append(
				patches,
				manifestPatch("ConfigMap", "foc-config", operations),
				manifestPatch(
					"ServiceAccount",
					"order-service",
					[]any{
						map[string]any{
							"op":    "add",
							"path":  "/metadata/annotations",
							"value": map[string]any{"eks.amazonaws.com/role-arn": config["order_role_arn"]},
						},
					},
				),
				manifestPatch(
					"StatefulSet",
					"kafka",
					[]any{
						map[string]any{
							"op":    "add",
							"path":  "/spec/volumeClaimTemplates/0/spec/storageClassName",
							"value": "foc-gp3",
						},
					},
				),
			)
			storage := map[string]any{
				"apiVersion":           "storage.k8s.io/v1",
				"kind":                 "StorageClass",
				"metadata":             map[string]any{"name": "foc-gp3"},
				"provisioner":          "ebs.csi.aws.com",
				"parameters":           map[string]any{"type": "gp3", "encrypted": "true"},
				"volumeBindingMode":    "WaitForFirstConsumer",
				"reclaimPolicy":        "Retain",
				"allowVolumeExpansion": true,
			}
			if err := saveJSON(a.root, filepath.Join(directory, "storage-class.json"), storage); err != nil {
				return err
			}
			certificate := map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata":   map[string]any{"name": "rds-ca", "namespace": "foc"},
				"data":       map[string]any{"global-bundle.pem": ca},
			}
			if err := saveJSON(a.root, filepath.Join(directory, "rds-ca.json"), certificate); err != nil {
				return err
			}
			resources = append(resources, "storage-class.json", "rds-ca.json")
		case "applications", "initialization":
			for _, service := range services {
				kind, name := "Deployment", service+"-service"
				if stage == "initialization" {
					kind, name = "Job", service+"-migrate"
				}
				patches = append(
					patches,
					map[string]any{
						"patch": compactJSON(
							certificatePatch(
								kind,
								name,
								name,
								kind == "Deployment" && service == "communication" && sender != "",
							),
						),
					},
				)
			}
		case "observability":
			// Helm owns Loki; the platform administrator owns Alloy's cross-namespace RBAC.
			patches = append(
				patches,
				manifestPatch(
					"PersistentVolumeClaim",
					"",
					[]any{map[string]any{"op": "add", "path": "/spec/storageClassName", "value": "foc-gp3"}},
				),
			)
		}
		kustomization["resources"] = resources
		if len(patches) > 0 {
			kustomization["patches"] = patches
		}
		if err := a.saveKustomization(directory, kustomization); err != nil {
			return err
		}
	}
	if err := a.saveKustomization(
		destination,
		map[string]any{"apiVersion": "kustomize.config.k8s.io/v1beta1", "kind": "Kustomization", "resources": stages},
	); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(a.out, "Rendered six deployment stages in", destination)
	for _, setting := range []struct{ env, key string }{{"K8S_IMAGE_PREFIX", "image_prefix"}, {"K8S_LOKI_BUCKET", "loki_bucket"}, {"K8S_LOKI_REGION", "region"}, {"K8S_LOKI_IRSA_ROLE", "loki_role_arn"}} {
		_, _ = fmt.Fprintf(a.out, "%s=%s\n", setting.env, stringValue(config, setting.key))
	}
	_, _ = fmt.Fprintln(a.out, "K8S_LOKI_STORAGE_CLASS=foc-gp3")
	_, _ = fmt.Fprintln(a.out, "Build for linux/amd64; choose a new immutable K8S_IMAGE_TAG for each image build.")
	return nil
}
