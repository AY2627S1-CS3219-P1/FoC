package location_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestRPCSharedDependencyBoundary(t *testing.T) {
	const root = "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/"
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", root+"rpc/location/shared").CombinedOutput()
	if err != nil {
		t.Fatalf("resolve imports: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, forbidden := range []string{root + "location/disablement", root + "location/additionrequest", root + "location/admin", root + "location/discovery", root + "rpc/location/disablement", root + "rpc/location/additionrequest", root + "database/", "github.com/jackc/pgx/"} {
			if strings.HasPrefix(dep, forbidden) {
				t.Errorf("RPC shared imports %s", dep)
			}
		}
	}
}
