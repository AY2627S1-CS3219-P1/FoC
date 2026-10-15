package seed

import (
	"path/filepath"
	"testing"
)

func TestDemoCSVsReuseImporterValidation(t *testing.T) {
	directory := filepath.Join("..", "..", "..", "data", "demo")
	paths := Paths{Suppliers: filepath.Join(directory, "suppliers.csv"), Buildings: filepath.Join(directory, "buildings.csv"), OrdinaryLocations: filepath.Join(directory, "locations.csv")}
	if _, err := load(paths); err == nil {
		t.Fatal("normal importer must still require supplier opening hours")
	}
	paths.AllowUnspecifiedHours = true
	data, err := load(paths)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := demoSourceIDs(data)
	if err != nil {
		t.Fatal(err)
	}
	if ids["supplier:demo-11"] != demoID(4, 11) {
		t.Fatal("new TOMORO fixture ID changed")
	}
	contacts, unspecified := 0, 0
	for _, l := range data.locations {
		if l.contact != nil {
			contacts++
		}
		if l.isSupplier && l.openFrom == nil {
			unspecified++
		}
	}
	if contacts != 5 || unspecified != 2 {
		t.Fatalf("contacts=%d unspecified supplier hours=%d", contacts, unspecified)
	}
	data.locations[0].sourceKey = "supplier:demo-99"
	if _, err := demoSourceIDs(data); err == nil {
		t.Fatal("out-of-range fixture ID accepted")
	}
}
