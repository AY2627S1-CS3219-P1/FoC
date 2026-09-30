package seed

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRequiresExactHeaders(t *testing.T) {
	directory := t.TempDir()
	buildings := filepath.Join(directory, "buildings.csv")
	suppliers := filepath.Join(directory, "suppliers.csv")
	locations := filepath.Join(directory, "locations.csv")

	writeText(t, buildings, strings.Join(buildingHeaders, ",")+"\n"+
		"building:com2,COM2,Com 2,1.294,103.774,100\n")
	invalidHeaders := append([]string(nil), supplierHeaders...)
	invalidHeaders[2] = "Category"
	writeText(t, suppliers, strings.Join(invalidHeaders, ",")+"\n")

	_, err := load(Paths{Suppliers: suppliers, Buildings: buildings, OrdinaryLocations: locations})
	if err == nil || !strings.Contains(err.Error(), "headers must be exactly") {
		t.Fatalf("Load error = %v, want exact-header failure", err)
	}
}

func TestLoadRejectsImplicitBuildingAliases(t *testing.T) {
	directory := t.TempDir()
	buildings := filepath.Join(directory, "buildings.csv")
	suppliers := filepath.Join(directory, "suppliers.csv")
	locations := filepath.Join(directory, "locations.csv")

	writeText(t, buildings, strings.Join(buildingHeaders, ",")+"\n"+
		"building:com2,COM2,Com 2,1.294,103.774,100\n")
	writeText(t, suppliers, strings.Join(supplierHeaders, ",")+"\n"+
		"supplier:test,Test Supplier,Food,Computing 2,1,Details,1.294,103.774,0900hrs,1800hrs,\n")

	_, err := load(Paths{Suppliers: suppliers, Buildings: buildings, OrdinaryLocations: locations})
	if err == nil || !strings.Contains(err.Error(), "has no explicit alias") {
		t.Fatalf("Load error = %v, want explicit-alias failure", err)
	}
}

func writeText(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
