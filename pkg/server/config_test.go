package server

import (
	"net/http"
	"testing"
)

func TestConfig(t *testing.T) {
	for _, tc := range []struct {
		name, mode, port, public, probe string
		listeners                       int
		invalid                         bool
	}{
		{name: "default", public: ":8080", probe: "http://127.0.0.1:8080", listeners: 1},
		{name: "Compose custom port", mode: "single", port: "9090", public: ":9090", probe: "http://127.0.0.1:9090", listeners: 1},
		{name: "mesh fixed loopback ports", mode: "mesh", port: "9090", public: "127.0.0.1:8080", probe: "http://127.0.0.1:8080", listeners: 2},
		{name: "trim config", mode: " mesh ", port: " 8080 ", public: "127.0.0.1:8080", probe: "http://127.0.0.1:8080", listeners: 2},
		{name: "typo", mode: "meshh", invalid: true},
		{name: "case mismatch", mode: "Mesh", invalid: true},
		{name: "zero port", port: "0", invalid: true},
		{name: "too large port", port: "65536", invalid: true},
		{name: "nonnumeric port", port: "tcp", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := LoadConfig(func(key string) string {
				return map[string]string{"RPC_MODE": tc.mode, "PORT": tc.port}[key]
			})
			if tc.invalid {
				if err == nil {
					t.Fatal("accepted invalid config")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			servers := cfg.Servers(http.NotFoundHandler(), nil)
			if cfg.PublicAddress() != tc.public || cfg.ProbeURL() != tc.probe || len(servers) != tc.listeners {
				t.Fatalf("address=%s probe=%s listeners=%d", cfg.PublicAddress(), cfg.ProbeURL(), len(servers))
			}
			if len(servers) == 2 && servers[1].Addr != "127.0.0.1:8081" {
				t.Fatal("internal listener is not loopback:8081")
			}
		})
	}
}

func TestCommandSyntax(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want Command
	}{
		{nil, Serve},
		{[]string{"migrate"}, Migrate},
		{[]string{"probe", "live"}, Live},
		{[]string{"probe", "ready"}, Ready},
		{[]string{"serve"}, ""},
		{[]string{"migrate", "down"}, ""},
		{[]string{"probe"}, ""},
		{[]string{"probe", "unknown"}, ""},
		{[]string{"probe", "ready", "extra"}, ""},
	} {
		got, err := ParseCommand(tc.args)
		if (tc.want == "") != (err != nil) || got != tc.want {
			t.Fatalf("args=%v command=%s err=%v", tc.args, got, err)
		}
	}
}
