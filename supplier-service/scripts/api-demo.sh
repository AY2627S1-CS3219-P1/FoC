#!/usr/bin/env bash
# Supplier Service API demo. Proves the Location APIs work without the
# frontend: discovery, role checks, and the admin create/update/archive
# lifecycle, each checked against the expected Connect response.
#
# Usage:
#   USER_TOKEN=<access token> ADMIN_TOKEN=<access token> scripts/api-demo.sh
#
# Tokens are User Service access tokens for a `user` and an `admin` (or
# `super_admin`) account. SUPPLIER_BASE_URL defaults to the Compose port.
# Set PAUSE=1 to wait for Enter between steps when presenting.
#
# Each run creates one uniquely named Location and archives it at the end,
# so normal discovery is unchanged and the script can be rerun.
set -euo pipefail

BASE_URL=${SUPPLIER_BASE_URL:-http://localhost:8082}
DISCOVERY=supplier.location.v1.LocationDiscoveryService
ADMIN=supplier.location.v1.LocationAdminService

: "${USER_TOKEN:?set USER_TOKEN to a User Service access token for a user}"
: "${ADMIN_TOKEN:?set ADMIN_TOKEN to a User Service access token for an admin}"
for tool in curl jq uuidgen; do
	command -v "$tool" >/dev/null || { echo "missing required tool: $tool" >&2; exit 2; }
done

body_file=$(mktemp)
trap 'rm -f "$body_file"' EXIT
STATUS=
BODY=
step_count=0

step() {
	step_count=$((step_count + 1))
	if [[ ${PAUSE:-0} == 1 && $step_count -gt 1 ]]; then
		read -r -p $'\n[Enter] next step ' _
	fi
	printf '\n\033[1m%d. %s\033[0m\n' "$step_count" "$1"
}

pass() { printf '   \033[32mok\033[0m %s\n' "$1"; }

fail() {
	printf '   \033[31mFAIL\033[0m %s\n' "$1" >&2
	printf '   HTTP %s: %s\n' "$STATUS" "$BODY" >&2
	exit 1
}

# rpc TOKEN SERVICE/METHOD JSON sends one Connect unary request. Pass "" as
# TOKEN to send no Authorization header.
rpc() {
	local token=$1 method=$2 json=$3
	local auth=()
	[[ -n $token ]] && auth=(-H "Authorization: Bearer $token")
	printf '   POST /%s %s\n' "$method" "$json"
	STATUS=$(curl -sS -o "$body_file" -w '%{http_code}' -X POST \
		-H 'Content-Type: application/json' "${auth[@]}" \
		--data "$json" "$BASE_URL/$method")
	BODY=$(<"$body_file")
}

expect_ok() {
	[[ $STATUS == 200 ]] || fail "expected success"
}

# expect_error CODE checks the Connect error code, e.g. permission_denied.
# The auth middleware rejects missing tokens before Connect runs, with HTTP
# 401 and the REST error envelope, so that case is matched by status.
expect_error() {
	local code
	code=$(jq -r '.code // empty' <<<"$BODY" 2>/dev/null || true)
	[[ -z $code && $STATUS == 401 ]] && code=unauthenticated
	[[ $STATUS != 200 && $code == "$1" ]] || fail "expected $1"
	pass "$1 (HTTP $STATUS): $(jq -r '.message // .status[0].message // ""' <<<"$BODY")"
}

field() { jq -r "$1" <<<"$BODY"; }

# total_items reads ListLocations totalItems. Proto JSON omits zero values
# and empty lists, so filters default them.
total_items() { field '.totalItems // "0"'; }

demo_name="API Demo Kiosk $(date +%Y%m%d-%H%M%S)"

step "Requests without a token are rejected"
rpc "" "$DISCOVERY/ListLocations" '{}'
expect_error unauthenticated

step "A user browses reference data for filters"
rpc "$USER_TOKEN" "$DISCOVERY/ListBuildings" '{}'
expect_ok
building_id=$(field '.buildings[0].id')
pass "$(field '.buildings | length') Buildings, using $(field '.buildings[0].name')"
rpc "$USER_TOKEN" "$DISCOVERY/ListCategories" '{}'
expect_ok
category_id=$(field '.categories[0].id')
pass "$(field '.categories | length') Categories, using $(field '.categories[0].name')"

step "A user searches suppliers by fuzzy, case-insensitive name"
rpc "$USER_TOKEN" "$DISCOVERY/ListLocations" '{"search":"CO-OP","suppliersOnly":true}'
expect_ok
[[ $(total_items) -ge 1 ]] || fail "expected at least one match"
pass "$(total_items) match: $(field '[(.locations // [])[].name] | join(", ")')"

step "A user lists page 2, sorted by Building"
rpc "$USER_TOKEN" "$DISCOVERY/ListLocations" \
	'{"sortField":"LOCATION_SORT_FIELD_BUILDING","page":2,"pageSize":5}'
expect_ok
pass "page $(field '.page') of $(field '.totalPages'), $(total_items) active Locations"

step "A user cannot see archived Locations"
rpc "$USER_TOKEN" "$DISCOVERY/ListLocations" '{"statusView":"LOCATION_STATUS_VIEW_ARCHIVED"}'
expect_error permission_denied

step "A user cannot call admin RPCs"
rpc "$USER_TOKEN" "$ADMIN/ArchiveLocation" "{\"id\":\"$(uuidgen)\"}"
expect_error permission_denied

create_key=$(uuidgen)
create_body=$(jq -nc --arg key "$create_key" --arg name "$demo_name" \
	--arg building "$building_id" --arg category "$category_id" '{
	idempotencyKey: $key,
	location: {
		name: $name, isSupplier: true, categoryIds: [$category],
		buildingId: $building, floor: "1", details: "Created by the API demo",
		coordinates: {latitude: 1.2966, longitude: 103.7764},
		opensAt: {hours: 8}, closesAt: {hours: 20}
	}}')

step "An admin creates a Supplier Location"
rpc "$ADMIN_TOKEN" "$ADMIN/CreateLocation" "$create_body"
expect_ok
location_id=$(field '.location.id')
[[ $(field '.location.revision') == 1 ]] || fail "expected revision 1"
pass "created $location_id at revision 1"

step "Retrying the create with the same key returns the same Location"
rpc "$ADMIN_TOKEN" "$ADMIN/CreateLocation" "$create_body"
expect_ok
[[ $(field '.location.id') == "$location_id" ]] || fail "expected the original ID"
pass "same ID, no duplicate"

step "Reusing the key with different input is rejected"
rpc "$ADMIN_TOKEN" "$ADMIN/CreateLocation" "$(jq -c '.location.floor = "2"' <<<"$create_body")"
expect_error already_exists

step "Invalid input is rejected: a Supplier needs a Category"
rpc "$ADMIN_TOKEN" "$ADMIN/CreateLocation" \
	"$(jq -c --arg key "$(uuidgen)" '.idempotencyKey = $key | .location.categoryIds = []' <<<"$create_body")"
expect_error failed_precondition

step "An admin updates floor and contact without touching other fields"
rpc "$ADMIN_TOKEN" "$ADMIN/UpdateLocation" "$(jq -nc --arg id "$location_id" '{
	id: $id, expectedRevision: "1", updateMask: "floor,contact",
	location: {floor: "B1", contact: "demo@example.com"}}')"
expect_ok
[[ $(field '.location.revision') == 2 && $(field '.location.floor') == B1 ]] ||
	fail "expected floor B1 at revision 2"
[[ $(field '.location.name') == "$demo_name" ]] || fail "expected the name unchanged"
pass "floor B1, contact set, name unchanged, revision 2"

step "An update with a stale revision is rejected"
rpc "$ADMIN_TOKEN" "$ADMIN/UpdateLocation" "$(jq -nc --arg id "$location_id" '{
	id: $id, expectedRevision: "1", updateMask: "details",
	location: {details: "This edit started from revision 1"}}')"
expect_error aborted

step "An admin archives the Location"
rpc "$ADMIN_TOKEN" "$ADMIN/ArchiveLocation" "{\"id\":\"$location_id\"}"
expect_ok
[[ $(field '.location.archivedAt // empty') ]] || fail "expected archivedAt"
pass "archived at $(field '.location.archivedAt')"

search_body=$(jq -nc --arg name "$demo_name" '{search: $name}')

step "Archived Locations leave normal discovery"
rpc "$USER_TOKEN" "$DISCOVERY/ListLocations" "$search_body"
expect_ok
[[ $(field "[(.locations // [])[] | select(.id == \"$location_id\")] | length") == 0 ]] ||
	fail "expected the Location to be hidden"
pass "not in the user's results"

step "Direct lookup still returns it, with archivedAt"
rpc "$USER_TOKEN" "$DISCOVERY/GetLocation" "{\"id\":\"$location_id\"}"
expect_ok
[[ $(field '.location.archivedAt // empty') ]] || fail "expected archivedAt"
pass "found, archived at $(field '.location.archivedAt')"

step "An admin sees it in the archived view"
rpc "$ADMIN_TOKEN" "$DISCOVERY/ListLocations" \
	"$(jq -c '.statusView = "LOCATION_STATUS_VIEW_ARCHIVED"' <<<"$search_body")"
expect_ok
[[ $(field "[(.locations // [])[] | select(.id == \"$location_id\")] | length") == 1 ]] ||
	fail "expected the Location in the archived view"
pass "listed as archived"

step "An admin restores it, and it returns to discovery"
rpc "$ADMIN_TOKEN" "$ADMIN/UnarchiveLocation" "{\"id\":\"$location_id\"}"
expect_ok
rpc "$USER_TOKEN" "$DISCOVERY/ListLocations" "$search_body"
expect_ok
[[ $(field "[(.locations // [])[] | select(.id == \"$location_id\")] | length") == 1 ]] ||
	fail "expected the Location back in discovery"
pass "back in the user's results"

step "Clean up: archive the demo Location"
rpc "$ADMIN_TOKEN" "$ADMIN/ArchiveLocation" "{\"id\":\"$location_id\"}"
expect_ok
pass "archived; normal discovery is unchanged"

printf '\n\033[32mAll %d steps passed.\033[0m\n' "$step_count"
