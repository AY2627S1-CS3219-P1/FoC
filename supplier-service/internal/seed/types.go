// Package seed imports the committed Supplier Service seed data.
package seed

import "time"

// Paths identifies every input required by the seed importer.
type Paths struct {
	Suppliers         string
	Buildings         string
	OrdinaryLocations string
}

// Counts reports how many resources were inserted or updated.
type Counts struct {
	Inserted int
	Updated  int
}

// Report summarizes the transaction by resource and Location classification.
type Report struct {
	Buildings          Counts
	Categories         Counts
	SupplierLocations  Counts
	OrdinaryLocations  Counts
	LocationCategories Counts
}

type dataset struct {
	buildings  []building
	categories []category
	locations  []location
}

type building struct {
	sourceKey string
	name      string
	latitude  float64
	longitude float64
	radiusM   float32
}

type category struct {
	sourceKey string
	name      string
}

type location struct {
	sourceKey   string
	name        string
	buildingKey string
	floor       *string
	latitude    float64
	longitude   float64
	openFrom    *time.Time
	openTo      *time.Time
	contact     *string
	details     string
	categories  []string
	isSupplier  bool
}
