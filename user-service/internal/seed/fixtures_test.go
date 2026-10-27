package seed

import (
	"os"
	"strings"
	"testing"
)

func testFixtures(t *testing.T) Fixtures {
	t.Helper()
	file, err := os.Open("../../../data/seeds/users.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close() //nolint:errcheck
	fixtures, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	return fixtures
}

func TestLoadFixtures(t *testing.T) {
	fixtures := testFixtures(t)
	if len(fixtures.Users) != 6 {
		t.Fatalf("got %d users, want six demo accounts", len(fixtures.Users))
	}
	for _, input := range []string{
		`{"version":2,"users":[]}`,
		`{"version":1,"unknown":true}`,
		`{} {}`,
	} {
		if _, err := Load(strings.NewReader(input)); err == nil {
			t.Fatalf("accepted invalid fixtures: %s", input)
		}
	}
	fixtures.Users[1].Email = fixtures.Users[0].Email
	if err := fixtures.Validate(); err == nil {
		t.Fatal("accepted duplicate fixture email")
	}
}
