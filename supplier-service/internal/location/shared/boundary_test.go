package shared_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestDomainSharedDependencyBoundary(t *testing.T) {
	// Shared Location types may use API errors, but must not depend on storage,
	// RPC handlers, generated service contracts, or capability implementations.
	// Go resolves transitive production imports, not just source-file imports.
	output, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("resolve dependencies: %v\n%s", err, output)
	}
	const project = "github.com/AY2627S1-CS3219-P1/FoC/"
	for _, dependency := range strings.Fields(string(output)) {
		for _, forbidden := range []string{
			"github.com/jackc/pgx/",
			project + "pkg/gen/",
			project + "supplier-service/internal/rpc/", project + "supplier-service/internal/database/",
			project + "supplier-service/internal/location/discovery", project + "supplier-service/internal/location/lifecycle",
		} {
			if strings.HasPrefix(dependency, forbidden) {
				t.Errorf("domain shared imports forbidden dependency %s", dependency)
			}
		}
	}
}
