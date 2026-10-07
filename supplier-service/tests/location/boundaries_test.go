package location_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCapabilitiesDoNotImportEachOther(t *testing.T) {
	const root = "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/"
	for _, capability := range []string{"disablement", "additionrequest"} {
		t.Run(capability, func(t *testing.T) {
			output, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", root+capability).CombinedOutput()
			if err != nil {
				t.Fatalf("resolve imports: %v\n%s", err, output)
			}
			for _, dependency := range strings.Fields(string(output)) {
				for _, other := range []string{"disablement", "additionrequest", "admin", "discovery", "lifecycle"} {
					if other != capability && strings.HasPrefix(dependency, root+other) {
						t.Errorf("%s imports %s", capability, dependency)
					}
				}
			}
		})
	}
}
