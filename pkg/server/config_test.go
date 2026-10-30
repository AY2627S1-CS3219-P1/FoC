package server

import (
	"net/http"
	"testing"
)

func TestConfig(t *testing.T) {
	for _, tc := range []struct {
		name         string
		external     string
		internal     string
		wantExternal string
		wantInternal string
		listeners    int
		invalid      bool
	}{
		{name: "default", wantExternal: ":8080", listeners: 1},
		{name: "custom external", external: "9090", wantExternal: ":9090", listeners: 1},
		{name: "separate internal", external: "9090", internal: "8081", wantExternal: ":9090", wantInternal: ":8081", listeners: 2},
		{name: "trim config", external: " 8080 ", internal: " 8081 ", wantExternal: ":8080", wantInternal: ":8081", listeners: 2},
		{name: "shared port", external: "8080", internal: "8080", invalid: true},
		{name: "zero port", external: "0", invalid: true},
		{name: "too large port", external: "65536", invalid: true},
		{name: "nonnumeric port", external: "tcp", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := LoadConfig(func(key string) string {
				return map[string]string{"EXTERNAL_PORT": tc.external, "INTERNAL_PORT": tc.internal}[key]
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
			handler := http.NotFoundHandler()
			servers, err := cfg.Servers(Handlers{Public: handler, Internal: handler, All: handler})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ExternalAddress() != tc.wantExternal || len(servers) != tc.listeners {
				t.Fatalf("external=%s listeners=%d", cfg.ExternalAddress(), len(servers))
			}
			if len(servers) == 2 && servers[1].Addr != tc.wantInternal {
				t.Fatalf("internal address=%s, want %s", servers[1].Addr, tc.wantInternal)
			}
		})
	}
}

func TestServersRequireHandlersForConfiguredPorts(t *testing.T) {
	handler := http.NotFoundHandler()
	if _, err := (Config{ExternalPort: "8080"}).Servers(Handlers{Public: handler}); err == nil {
		t.Fatal("accepted missing all-routes handler without an internal listener")
	}
	if _, err := (Config{ExternalPort: "8080", InternalPort: "8081"}).Servers(Handlers{Public: handler}); err == nil {
		t.Fatal("accepted missing internal handler with an internal listener")
	}
	if _, err := (Config{ExternalPort: "8080"}).Servers(Handlers{All: handler}); err == nil {
		t.Fatal("accepted missing public handler")
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
