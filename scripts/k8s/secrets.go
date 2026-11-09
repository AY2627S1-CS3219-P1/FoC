package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Credentials are write-once: never silently rotate a password or signing key
// while the corresponding database/PVC still exists.
func persistValue(directory, name, value string, allowEmpty bool) error {
	path := filepath.Join(directory, name)
	info, err := os.Stat(path)
	if err == nil {
		// An empty optional provider key is an unconfigured value, not a credential to preserve.
		if name == "CMS_LLM_API_KEY" && allowEmpty && info.Size() == 0 && strings.TrimSpace(value) != "" {
			if err := os.Chmod(path, 0600); err != nil {
				return err
			}
			return writePrivate(path, value)
		}
		if info.Size() == 0 && !allowEmpty {
			return fmt.Errorf("empty credential %s; restore it rather than rotating a database password", path)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(value); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func (o *operator) applySecret(namespace, name string, options ...string) error {
	args := append(o.kubePrefix(), "-n", namespace, "create", "secret", "generic", name)
	args = append(args, options...)
	args = append(args, "--dry-run=client", "-o", "yaml")
	result, err := o.run("kubectl", args, commandOptions{capture: true})
	if err != nil {
		return err
	}
	path := filepath.Join(o.state, "secrets", "."+name+".yaml")
	if err := writePrivate(path, result.stdout); err != nil {
		return err
	}
	defer removeTemporary(path)
	return o.applyManifest(path)
}

func (o *operator) generateLocalSecrets() error {
	if err := requireCommands("kubectl", "openssl"); err != nil {
		return err
	}
	if err := o.prepareState(); err != nil {
		return err
	}
	directory := filepath.Join(o.state, "secrets")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return err
	}
	lock := filepath.Join(directory, ".generating")
	if err := os.Mkdir(lock, 0700); err != nil {
		return fmt.Errorf("cannot acquire %s; inspect for another generator or a stale lock: %w", lock, err)
	}
	defer removeTemporary(lock)

	for _, name := range []string{"postgres-password", "user-password", "supplier-password", "order-password", "credit-password", "communication-password", "moderation-password", "kafka-ui-password", "grafana-password"} {
		password, err := o.run("openssl", []string{"rand", "-hex", "32"}, commandOptions{capture: true})
		if err != nil {
			return err
		}
		if err := persistValue(directory, name, strings.TrimSpace(password.stdout), false); err != nil {
			return err
		}
	}
	for _, entry := range []struct{ name, value string }{
		{"grafana-user", "operator"}, {"postgres-user", "foc"}, {"kafka-ui-user", "operator"}, {"SMTP_ADDR", "mailpit:1025"}, {"SMTP_FROM", "FoC <no-reply@foc.local>"},
	} {
		if err := persistValue(directory, entry.name, entry.value, false); err != nil {
			return err
		}
	}
	for _, entry := range []struct{ name, value string }{
		{"SMTP_USERNAME", ""}, {"SMTP_PASSWORD", ""}, {"TELEGRAM_BOT_TOKEN", os.Getenv("K8S_TELEGRAM_BOT_TOKEN")},
		{"CMS_LLM_API_KEY", os.Getenv("K8S_LLM_API_KEY")},
	} {
		if err := persistValue(directory, entry.name, entry.value, true); err != nil {
			return err
		}
	}

	privateKey := filepath.Join(directory, "jwt-signing-private.pem")
	if _, err := os.Stat(privateKey); errors.Is(err, os.ErrNotExist) {
		temporary := filepath.Join(lock, "jwt.pem")
		if err := o.execute(
			"openssl",
			"genpkey",
			"-algorithm",
			"EC",
			"-pkeyopt",
			"ec_paramgen_curve:P-256",
			"-out",
			temporary,
		); err != nil {
			return err
		}
		if err := os.Chmod(temporary, 0600); err != nil {
			return err
		}
		if err := os.Rename(temporary, privateKey); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if _, err := o.run(
		"openssl",
		[]string{"pkey", "-in", privateKey, "-check", "-noout"},
		commandOptions{capture: true},
	); err != nil {
		return err
	}

	tlsKey, tlsCert := filepath.Join(directory, "tls.key"), filepath.Join(directory, "tls.crt")
	_, keyErr := os.Stat(tlsKey)
	_, certErr := os.Stat(tlsCert)
	if errors.Is(keyErr, os.ErrNotExist) && errors.Is(certErr, os.ErrNotExist) {
		temporaryKey, temporaryCert := filepath.Join(lock, "tls.key"), filepath.Join(lock, "tls.crt")
		config := filepath.Join(lock, "tls.cnf")
		if err := writePrivate(
			config,
			"[req]\nprompt=no\ndistinguished_name=dn\nx509_extensions=ext\n[dn]\nCN=localhost\n[ext]\nbasicConstraints=critical,CA:TRUE\nsubjectAltName=DNS:localhost,IP:127.0.0.1\n",
		); err != nil {
			return err
		}
		if _, err := o.run(
			"openssl",
			[]string{
				"req",
				"-x509",
				"-newkey",
				"rsa:3072",
				"-sha256",
				"-nodes",
				"-days",
				"365",
				"-config",
				config,
				"-keyout",
				temporaryKey,
				"-out",
				temporaryCert,
			},
			commandOptions{capture: true},
		); err != nil {
			return err
		}
		for _, pair := range [][2]string{{temporaryKey, tlsKey}, {temporaryCert, tlsCert}} {
			if err := os.Chmod(pair[0], 0600); err != nil {
				return err
			}
			if err := os.Rename(pair[0], pair[1]); err != nil {
				return err
			}
		}
	}
	for _, path := range []string{tlsKey, tlsCert} {
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			return errors.New("incomplete local TLS keypair; restore its matching files")
		}
	}
	if _, err := o.run(
		"openssl",
		[]string{"x509", "-in", tlsCert, "-noout", "-checkend", "0"},
		commandOptions{capture: true},
	); err != nil {
		return err
	}
	certificate, err := o.run(
		"openssl",
		[]string{"x509", "-in", tlsCert, "-pubkey", "-noout"},
		commandOptions{capture: true},
	)
	if err != nil {
		return err
	}
	key, err := o.run("openssl", []string{"pkey", "-in", tlsKey, "-pubout"}, commandOptions{capture: true})
	if err != nil {
		return err
	}
	if certificate.stdout != key.stdout {
		return errors.New("local TLS certificate and key do not match")
	}

	for _, service := range []string{"user", "supplier", "order", "credit", "communication", "moderation"} {
		if err := persistValue(directory, service+"-db-user", service+"_app", false); err != nil {
			return err
		}
		if err := persistValue(directory, service+"-db-name", service+"_db", false); err != nil {
			return err
		}
		var fields []string
		for _, suffix := range []string{"-db-user", "-db-name", "-password"} {
			value, err := os.ReadFile(filepath.Join(directory, service+suffix))
			if err != nil {
				return err
			}
			fields = append(fields, strings.TrimSpace(string(value)))
		}
		url := "postgres://" + fields[0] + ":" + fields[2] + "@postgres:5432/" + fields[1] + "?sslmode=disable"
		if err := persistValue(directory, service+"-database-url", url, false); err != nil {
			return err
		}
	}
	fromFile := func(key, file string) string { return "--from-file=" + key + "=" + filepath.Join(directory, file) }
	if err := o.applySecret(
		"foc",
		"postgres-admin",
		fromFile("POSTGRES_USER", "postgres-user"),
		fromFile("POSTGRES_PASSWORD", "postgres-password"),
	); err != nil {
		return err
	}
	for _, service := range []string{"user", "supplier", "order", "credit", "communication", "moderation"} {
		if err := o.applySecret(
			"foc",
			service+"-db",
			fromFile("DB_USER", service+"-db-user"),
			fromFile("DB_NAME", service+"-db-name"),
			fromFile("DB_PASSWORD", service+"-password"),
			fromFile("DATABASE_URL", service+"-database-url"),
		); err != nil {
			return err
		}
	}
	if err := o.applySecret(
		"foc",
		"jwt-signing",
		fromFile("jwt-signing-private.pem", "jwt-signing-private.pem"),
	); err != nil {
		return err
	}
	if err := o.applySecret(
		"foc",
		"smtp",
		fromFile("SMTP_ADDR", "SMTP_ADDR"),
		fromFile("SMTP_FROM", "SMTP_FROM"),
		fromFile("SMTP_USERNAME", "SMTP_USERNAME"),
		fromFile("SMTP_PASSWORD", "SMTP_PASSWORD"),
	); err != nil {
		return err
	}
	if err := o.applySecret("foc", "telegram", fromFile("TELEGRAM_BOT_TOKEN", "TELEGRAM_BOT_TOKEN")); err != nil {
		return err
	}
	// The model API key stays server-side. An empty key deploys CMS degraded:
	// submissions queue and admin APIs work, no inference attempts are burned.
	if err := o.applySecret("foc", "moderation-llm", fromFile("CMS_LLM_API_KEY", "CMS_LLM_API_KEY")); err != nil {
		return err
	}
	if err := o.applySecret(
		"foc",
		"object-storage",
		"--from-literal=AWS_ACCESS_KEY_ID=test",
		"--from-literal=AWS_SECRET_ACCESS_KEY=test",
	); err != nil {
		return err
	}
	if err := o.applySecret(
		"foc",
		"kafka-ui-auth",
		fromFile("SPRING_SECURITY_USER_NAME", "kafka-ui-user"),
		fromFile("SPRING_SECURITY_USER_PASSWORD", "kafka-ui-password"),
	); err != nil {
		return err
	}
	if err := o.applySecret(
		"istio-ingress",
		"foc-local-tls",
		"--type=kubernetes.io/tls",
		fromFile("tls.crt", "tls.crt"),
		fromFile("tls.key", "tls.key"),
	); err != nil {
		return err
	}
	if err := o.applySecret(
		"foc-observability",
		"grafana-admin",
		fromFile("username", "grafana-user"),
		fromFile("password", "grafana-password"),
	); err != nil {
		return err
	}
	origin, err := localOrigin()
	if err != nil {
		return err
	}
	note("Local secrets preserved in %s. Trust tls.crt for %s.", directory, origin)
	return nil
}
