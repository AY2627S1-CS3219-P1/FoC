package seed

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var sourceKeyPattern = regexp.MustCompile(`^[a-z0-9]+(?::[a-z0-9]+(?:-[a-z0-9]+)*)$`)

type categoryDefinition struct {
	sourceKey string
	name      string
}

var categoryDefinitions = map[string]categoryDefinition{
	"coffee":   {sourceKey: "category:coffee", name: "Coffee"},
	"food":     {sourceKey: "category:food", name: "Food"},
	"printing": {sourceKey: "category:printing", name: "Printing"},
	"shopping": {sourceKey: "category:shopping", name: "Shopping"},
}

var buildingHeaders = []string{
	"SourceKey", "Name", "Aliases", "Latitude", "Longitude", "RadiusM",
}

var supplierHeaders = []string{
	"SourceKey", "Name", "Type", "Building", "Floor", "Location Description",
	"Latitude", "Longitude", "StartingTime", "ClosingTime", "ImageURL",
}

var ordinaryLocationHeaders = []string{
	"SourceKey", "Name", "BuildingSourceKey", "Floor", "Location Description",
	"Latitude", "Longitude", "StartingTime", "ClosingTime",
}

type csvRecord struct {
	line   int
	values map[string]string
}

func load(paths Paths) (dataset, error) {
	buildings, aliases, err := loadBuildings(paths.Buildings)
	if err != nil {
		return dataset{}, err
	}

	suppliers, categories, sourceKeys, err := loadSuppliers(paths.Suppliers, aliases)
	if err != nil {
		return dataset{}, err
	}

	ordinary, err := loadOrdinaryLocations(paths.OrdinaryLocations, buildings, sourceKeys)
	if err != nil {
		return dataset{}, err
	}

	locations := append(suppliers, ordinary...)
	return dataset{
		buildings:  buildings,
		categories: categories,
		locations:  locations,
	}, nil
}

func loadBuildings(path string) ([]building, map[string]string, error) {
	records, err := readCSV(path, buildingHeaders)
	if err != nil {
		return nil, nil, err
	}
	if len(records) == 0 {
		return nil, nil, fmt.Errorf("%s: requires at least one Building", path)
	}

	buildings := make([]building, 0, len(records))
	aliases := make(map[string]string)
	sourceKeys := make(map[string]int)
	for _, record := range records {
		key, err := parseSourceKey(record, "SourceKey", "building")
		if err != nil {
			return nil, nil, pathError(path, record.line, err)
		}
		if firstLine, exists := sourceKeys[key]; exists {
			return nil, nil, pathError(path, record.line, fmt.Errorf("SourceKey %q duplicates line %d", key, firstLine))
		}
		sourceKeys[key] = record.line

		name, err := requiredText(record, "Name", 200)
		if err != nil {
			return nil, nil, pathError(path, record.line, err)
		}
		latitude, longitude, err := coordinates(record)
		if err != nil {
			return nil, nil, pathError(path, record.line, err)
		}
		radius, err := positiveFloat(record, "RadiusM")
		if err != nil {
			return nil, nil, pathError(path, record.line, err)
		}

		buildingAliases := splitAliases(record.values["Aliases"])
		buildingAliases = append(buildingAliases, name)
		for _, alias := range buildingAliases {
			normalized := normalizeAlias(alias)
			if normalized == "" {
				return nil, nil, pathError(path, record.line, errors.New("Aliases contains an empty alias"))
			}
			if existing, exists := aliases[normalized]; exists && existing != key {
				return nil, nil, pathError(path, record.line, fmt.Errorf("Building alias %q already belongs to %q", alias, existing))
			}
			aliases[normalized] = key
		}

		buildings = append(buildings, building{
			sourceKey: key,
			name:      name,
			latitude:  latitude,
			longitude: longitude,
			radiusM:   float32(radius),
		})
	}

	return buildings, aliases, nil
}

func loadSuppliers(path string, aliases map[string]string) ([]location, []category, map[string]int, error) {
	records, err := readCSV(path, supplierHeaders)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(records) == 0 {
		return nil, nil, nil, fmt.Errorf("%s: requires at least one Supplier", path)
	}

	locations := make([]location, 0, len(records))
	usedCategories := make(map[string]struct{})
	sourceKeys := make(map[string]int)
	for _, record := range records {
		key, err := parseSourceKey(record, "SourceKey", "supplier")
		if err != nil {
			return nil, nil, nil, pathError(path, record.line, err)
		}
		if firstLine, exists := sourceKeys[key]; exists {
			return nil, nil, nil, pathError(path, record.line, fmt.Errorf("SourceKey %q duplicates line %d", key, firstLine))
		}
		sourceKeys[key] = record.line

		buildingAlias, err := requiredText(record, "Building", 200)
		if err != nil {
			return nil, nil, nil, pathError(path, record.line, err)
		}
		buildingKey, exists := aliases[normalizeAlias(buildingAlias)]
		if !exists {
			return nil, nil, nil, pathError(path, record.line, fmt.Errorf("Building %q has no explicit alias", buildingAlias))
		}

		parsed, err := parseLocation(record, key, buildingKey, true)
		if err != nil {
			return nil, nil, nil, pathError(path, record.line, err)
		}
		if parsed.openFrom == nil {
			return nil, nil, nil, pathError(path, record.line, errors.New("Supplier opening hours are required"))
		}

		categoryValues := strings.Split(record.values["Type"], "/")
		if len(categoryValues) == 0 {
			return nil, nil, nil, pathError(path, record.line, errors.New("Type is required"))
		}
		seenCategories := make(map[string]struct{})
		for _, value := range categoryValues {
			name := strings.TrimSpace(value)
			if name == "" {
				return nil, nil, nil, pathError(path, record.line, errors.New("Type contains an empty Category"))
			}
			if utf8.RuneCountInString(name) > 200 {
				return nil, nil, nil, pathError(path, record.line, errors.New("Type Category exceeds 200 characters"))
			}
			normalized := normalizeAlias(name)
			if _, duplicate := seenCategories[normalized]; duplicate {
				return nil, nil, nil, pathError(path, record.line, fmt.Errorf("Type repeats Category %q", name))
			}
			definition, exists := categoryDefinitions[normalized]
			if !exists {
				return nil, nil, nil, pathError(path, record.line, fmt.Errorf("Type Category %q has no explicit stable source key", name))
			}
			seenCategories[normalized] = struct{}{}
			usedCategories[normalized] = struct{}{}
			parsed.categories = append(parsed.categories, definition.sourceKey)
		}
		locations = append(locations, parsed)
	}

	categories := make([]category, 0, len(usedCategories))
	for normalized := range usedCategories {
		definition := categoryDefinitions[normalized]
		categories = append(categories, category{
			sourceKey: definition.sourceKey,
			name:      definition.name,
		})
	}
	sort.Slice(categories, func(i, j int) bool {
		return categories[i].sourceKey < categories[j].sourceKey
	})

	return locations, categories, sourceKeys, nil
}

func loadOrdinaryLocations(path string, buildings []building, sourceKeys map[string]int) ([]location, error) {
	records, err := readCSV(path, ordinaryLocationHeaders)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("%s: requires at least one ordinary Location", path)
	}

	buildingKeys := make(map[string]struct{}, len(buildings))
	for _, building := range buildings {
		buildingKeys[building.sourceKey] = struct{}{}
	}

	locations := make([]location, 0, len(records))
	for _, record := range records {
		key, err := parseSourceKey(record, "SourceKey", "location")
		if err != nil {
			return nil, pathError(path, record.line, err)
		}
		if firstLine, exists := sourceKeys[key]; exists {
			return nil, pathError(path, record.line, fmt.Errorf("SourceKey %q duplicates line %d in another Location input", key, firstLine))
		}
		sourceKeys[key] = record.line

		buildingKey := strings.TrimSpace(record.values["BuildingSourceKey"])
		if _, exists := buildingKeys[buildingKey]; !exists {
			return nil, pathError(path, record.line, fmt.Errorf("BuildingSourceKey %q does not exist", buildingKey))
		}
		parsed, err := parseLocation(record, key, buildingKey, false)
		if err != nil {
			return nil, pathError(path, record.line, err)
		}
		locations = append(locations, parsed)
	}

	return locations, nil
}

func parseLocation(record csvRecord, sourceKey, buildingKey string, isSupplier bool) (location, error) {
	name, err := requiredText(record, "Name", 200)
	if err != nil {
		return location{}, err
	}
	floor, err := optionalText(record, "Floor", 50)
	if err != nil {
		return location{}, err
	}
	details, err := optionalText(record, "Location Description", 2000)
	if err != nil {
		return location{}, err
	}
	latitude, longitude, err := coordinates(record)
	if err != nil {
		return location{}, err
	}
	openFrom, openTo, err := openingHours(record)
	if err != nil {
		return location{}, err
	}

	detailValue := ""
	if details != nil {
		detailValue = *details
	}
	return location{
		sourceKey:   sourceKey,
		name:        name,
		buildingKey: buildingKey,
		floor:       floor,
		latitude:    latitude,
		longitude:   longitude,
		openFrom:    openFrom,
		openTo:      openTo,
		details:     detailValue,
		isSupplier:  isSupplier,
	}, nil
}

func readCSV(path string, expectedHeaders []string) ([]csvRecord, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	contents = bytes.TrimPrefix(contents, []byte{0xEF, 0xBB, 0xBF})
	if !utf8.Valid(contents) {
		return nil, fmt.Errorf("%s: CSV must be UTF-8", path)
	}

	reader := csv.NewReader(bytes.NewReader(contents))
	reader.FieldsPerRecord = len(expectedHeaders)
	headers, err := reader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s: empty CSV", path)
		}
		return nil, fmt.Errorf("read %s header: %w", path, err)
	}
	if !slices.Equal(headers, expectedHeaders) {
		return nil, fmt.Errorf("%s: headers must be exactly %q, got %q", path, expectedHeaders, headers)
	}

	var records []csvRecord
	for line := 2; ; line++ {
		values, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, pathError(path, line, readErr)
		}
		mapped := make(map[string]string, len(headers))
		for index, header := range headers {
			mapped[header] = values[index]
		}
		records = append(records, csvRecord{line: line, values: mapped})
	}
	return records, nil
}

func requiredText(record csvRecord, field string, maxLength int) (string, error) {
	value := strings.TrimSpace(record.values[field])
	if value == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if utf8.RuneCountInString(value) > maxLength {
		return "", fmt.Errorf("%s exceeds %d characters", field, maxLength)
	}
	return value, nil
}

func optionalText(record csvRecord, field string, maxLength int) (*string, error) {
	value := strings.TrimSpace(record.values[field])
	if value == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(value) > maxLength {
		return nil, fmt.Errorf("%s exceeds %d characters", field, maxLength)
	}
	return &value, nil
}

func parseSourceKey(record csvRecord, field, prefix string) (string, error) {
	value := strings.TrimSpace(record.values[field])
	if !sourceKeyPattern.MatchString(value) || !strings.HasPrefix(value, prefix+":") {
		return "", fmt.Errorf("%s must match %s:<stable-slug>", field, prefix)
	}
	return value, nil
}

func coordinates(record csvRecord) (float64, float64, error) {
	latitude, err := boundedFloat(record, "Latitude", -90, 90)
	if err != nil {
		return 0, 0, err
	}
	longitude, err := boundedFloat(record, "Longitude", -180, 180)
	if err != nil {
		return 0, 0, err
	}
	return latitude, longitude, nil
}

func boundedFloat(record csvRecord, field string, minimum, maximum float64) (float64, error) {
	value, err := strconv.ParseFloat(strings.TrimSpace(record.values[field]), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("%s must be a number", field)
	}
	if value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %v and %v", field, minimum, maximum)
	}
	return value, nil
}

func positiveFloat(record csvRecord, field string) (float64, error) {
	value, err := strconv.ParseFloat(strings.TrimSpace(record.values[field]), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive number", field)
	}
	return value, nil
}

func openingHours(record csvRecord) (*time.Time, *time.Time, error) {
	fromValue := strings.TrimSpace(record.values["StartingTime"])
	toValue := strings.TrimSpace(record.values["ClosingTime"])
	if (fromValue == "") != (toValue == "") {
		return nil, nil, errors.New("StartingTime and ClosingTime must both be set or both be empty")
	}
	if fromValue == "" {
		return nil, nil, nil
	}

	singapore, err := time.LoadLocation("Asia/Singapore")
	if err != nil {
		return nil, nil, fmt.Errorf("load Asia/Singapore timezone: %w", err)
	}
	openFrom, err := time.ParseInLocation("1504hrs", fromValue, singapore)
	if err != nil {
		return nil, nil, fmt.Errorf("StartingTime must use HHMMhrs: %w", err)
	}
	openTo, err := time.ParseInLocation("1504hrs", toValue, singapore)
	if err != nil {
		return nil, nil, fmt.Errorf("ClosingTime must use HHMMhrs: %w", err)
	}
	if openFrom.Hour() == openTo.Hour() && openFrom.Minute() == openTo.Minute() {
		return nil, nil, errors.New("StartingTime and ClosingTime must differ")
	}
	return &openFrom, &openTo, nil
}

func splitAliases(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, "|")
	aliases := make([]string, 0, len(parts))
	for _, part := range parts {
		aliases = append(aliases, strings.TrimSpace(part))
	}
	return aliases
}

func normalizeAlias(value string) string {
	value = strings.ReplaceAll(value, "’", "'")
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func pathError(path string, line int, err error) error {
	return fmt.Errorf("%s:%d: %w", path, line, err)
}
