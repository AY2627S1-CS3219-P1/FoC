# User Service – Demo runbook

This runbook shows authentication and role-based access control across the User and
Supplier services, using `curl` against the Connect JSON protocol. The design
behind each step is in [design.md](design.md).

The profile and admin RPCs are in PR #76/#106. Until they merge, check out
`iz/refactor/shared-role-authorization` before starting.

Connect returns errors as JSON (`{"code":"permission_denied", …}`) with a
matching HTTP status: `unauthenticated` → 401, `permission_denied` → 403,
`invalid_argument` → 400, `not_found` → 404, `failed_precondition` → 400.

## 0. Setup

In the repository root `.env` (see `.env.example`):

```bash
BOOTSTRAP_SUPERADMIN_EMAIL=admin@u.nus.edu
BOOTSTRAP_SUPERADMIN_DISPLAY_NAME=Demo Admin
```

Start the stack and allow the campus domain:

```bash
docker compose up --build -d postgres mailpit user-service supplier-service
docker compose logs user-service | grep "admin bootstrapped"   # outcome=created
docker exec foc-postgres psql -U foc -d user_dev \
  -c "INSERT INTO allowed_email_domains (domain) VALUES ('u.nus.edu');"
```

Helpers for a Bash shell (Git Bash on Windows):

```bash
US=http://localhost:8081   # User Service
SS=http://localhost:8082   # Supplier Service
MP=http://localhost:8025   # Mailpit

# rpc <base> <Service/Method> <json> [token]
rpc() {
  curl -s -w '\nHTTP %{http_code}\n' -X POST "$1/$2" \
    -H 'Content-Type: application/json' ${4:+-H "Authorization: Bearer $4"} -d "$3"
}

# Magic-link token from the newest email in Mailpit.
link_token() { curl -s "$MP/api/v1/message/latest" | grep -o 'token=[A-Za-z0-9_-]*' | head -1 | cut -d= -f2; }

# signin <email> [display name]: request a link, then log in or register;
# prints the access token.
signin() {
  rpc $US user.v1.AuthService/RequestLink "{\"email\":\"$1\"}" > /dev/null
  sleep 1
  local t; t=$(link_token)
  if [ -n "$2" ]; then
    rpc $US user.v1.AuthService/Register "{\"token\":\"$t\",\"displayName\":\"$2\"}"
  else
    rpc $US user.v1.AuthService/Login "{\"token\":\"$t\"}"
  fi | grep -o '"accessToken":"[^"]*"' | cut -d'"' -f4
}

psql_user() { docker exec foc-postgres psql -U foc -d user_dev -c "$1"; }
```

## 1. Sign in (authentication)

```bash
ADMIN=$(signin admin@u.nus.edu)            # bootstrapped super_admin: login
BOB=$(signin bob@u.nus.edu "Bob")          # new: register → role user
CAROL=$(signin carol@u.nus.edu "Carol")    # new: register → role user
```

Show the role claim inside a token (the payload is plain base64url):

```bash
echo "$BOB" | cut -d. -f2 | tr '_-' '/+' | base64 -d 2>/dev/null; echo   # "role":"user"
```

Show that a domain outside the whitelist is rejected:

```bash
rpc $US user.v1.AuthService/RequestLink '{"email":"eve@gmail.com"}'       # 401 unauthenticated
```

Point out on screen: Mailpit shows the emailed links; `psql_user "SELECT
purpose, used_at FROM auth_tokens"` shows only hashes are stored and the links
are now used.

## 2. No token → 401

```bash
rpc $US user.v1.ProfileService/GetMyProfile '{}'                          # 401 unauthenticated
rpc $US user.v1.ProfileService/GetMyProfile '{}' not.a.jwt                # 401 unauthenticated
```

## 3. Profile updates are validated and scoped to the caller

```bash
rpc $US user.v1.ProfileService/GetMyProfile '{}' "$BOB"                   # 200, Bob's own profile
rpc $US user.v1.ProfileService/UpdateMyProfile \
  '{"displayName":"Bob","description":"Courier","telegramHandle":"@bob"}' "$BOB"      # 400 invalid handle
rpc $US user.v1.ProfileService/UpdateMyProfile \
  '{"displayName":"Bob","description":"Courier","telegramHandle":"bob_tan"}' "$BOB"   # 200
```

The request has no `id` or `role` field. The target is always the token's
subject, and fields outside the contract are ignored:

```bash
rpc $US user.v1.ProfileService/UpdateMyProfile \
  '{"displayName":"Bob","role":"USER_ROLE_ADMIN"}' "$BOB"
psql_user "SELECT email, role FROM users"                                # Bob is still 'user'
```

## 4. A normal user cannot use admin RPCs → 403

```bash
rpc $US user.v1.UserAdminService/GetUserByEmail '{"email":"carol@u.nus.edu"}' "$BOB"   # 403
```

## 5. Promotion by the super admin (no developer involvement)

```bash
BOB_ID=$(rpc $US user.v1.UserAdminService/GetUserByEmail '{"email":"bob@u.nus.edu"}' "$ADMIN" \
  | grep -o '"id":"[^"]*"' | cut -d'"' -f4)
rpc $US user.v1.UserAdminService/ChangeUserRole \
  "{\"userId\":\"$BOB_ID\",\"toRole\":\"USER_ROLE_ADMIN\",\"reason\":\"New moderator\"}" "$ADMIN"   # 200
psql_user "SELECT from_role, to_role, reason, created_by FROM role_changes ORDER BY created_at"
```

## 6. Supplier Service enforces the same roles

Bob's **current** token still says `user`, because roles take effect when a new token is issued:

```bash
rpc $SS supplier.location.v1.LocationDiscoveryService/ListLocations '{}' "$BOB"      # 200 (any role)
rpc $SS supplier.location.v1.LocationDiscoveryService/ListLocations \
  '{"statusView":"LOCATION_STATUS_VIEW_ARCHIVED"}' "$BOB"                            # 403
rpc $SS supplier.location.v1.LocationAdminService/ArchiveLocation \
  '{"id":"00000000-0000-0000-0000-000000000000"}' "$BOB"                             # 403, even before validation
rpc $SS supplier.location.v1.LocationAdminService/ArchiveLocation '{}' ""            # 401
```

## 7. New token, new permissions

```bash
BOB=$(signin bob@u.nus.edu)                                                   # fresh login
echo "$BOB" | cut -d. -f2 | tr '_-' '/+' | base64 -d 2>/dev/null; echo        # "role":"admin"
rpc $SS supplier.location.v1.LocationDiscoveryService/ListLocations \
  '{"statusView":"LOCATION_STATUS_VIEW_ARCHIVED"}' "$BOB"                            # 200
rpc $SS supplier.location.v1.LocationAdminService/ArchiveLocation \
  '{"id":"00000000-0000-0000-0000-000000000000"}' "$BOB"                             # 404: authorized, no such Location
```

## 8. Admin limits and edge cases

```bash
CAROL_ID=$(rpc $US user.v1.UserAdminService/GetUserByEmail '{"email":"carol@u.nus.edu"}' "$BOB" \
  | grep -o '"id":"[^"]*"' | cut -d'"' -f4)
ADMIN_ID=$(rpc $US user.v1.UserAdminService/GetUserByEmail '{"email":"admin@u.nus.edu"}' "$ADMIN" \
  | grep -o '"id":"[^"]*"' | cut -d'"' -f4)

# An admin cannot create admins
rpc $US user.v1.UserAdminService/ChangeUserRole \
  "{\"userId\":\"$CAROL_ID\",\"toRole\":\"USER_ROLE_ADMIN\"}" "$BOB"                          # 403
# Suspension requires a reason
rpc $US user.v1.UserAdminService/ChangeUserRole \
  "{\"userId\":\"$CAROL_ID\",\"toRole\":\"USER_ROLE_SUSPENDED_USER\"}" "$BOB"                  # 400
rpc $US user.v1.UserAdminService/ChangeUserRole \
  "{\"userId\":\"$CAROL_ID\",\"toRole\":\"USER_ROLE_SUSPENDED_USER\",\"reason\":\"No-show x3\"}" "$BOB"   # 200
# Carol's old token still says "user", but the database check blocks her
rpc $US user.v1.ProfileService/UpdateMyProfile '{"displayName":"Carol"}' "$CAROL"            # 403
rpc $US user.v1.ProfileService/GetMyProfile '{}' "$CAROL"                                    # 200 (read-only)

# Nobody can change their own role, including the only super admin
rpc $US user.v1.UserAdminService/ChangeUserRole \
  "{\"userId\":\"$BOB_ID\",\"toRole\":\"USER_ROLE_USER\"}" "$BOB"                              # 403
rpc $US user.v1.UserAdminService/ChangeUserRole \
  "{\"userId\":\"$ADMIN_ID\",\"toRole\":\"USER_ROLE_USER\"}" "$ADMIN"                          # 403
# An admin cannot touch the super admin
rpc $US user.v1.UserAdminService/ChangeUserRole \
  "{\"userId\":\"$ADMIN_ID\",\"toRole\":\"USER_ROLE_SUSPENDED_USER\",\"reason\":\"x\"}" "$BOB" # 403
```

## 9. Bootstrap runs only once

```bash
BOOTSTRAP_SUPERADMIN_EMAIL=mallory@u.nus.edu docker compose up -d --force-recreate user-service
docker compose logs user-service | grep "already bootstrapped"
psql_user "SELECT u.email FROM admin_bootstrap b JOIN users u ON u.id = b.user_id"   # still admin@
```

## Reset

```bash
docker compose down -v   # removes the database volume
```

## Troubleshooting

- The first start after recreating containers is slow while Go modules
  download: [LANDMINES/go-cache.md](../../LANDMINES/go-cache.md).
- PostGIS fails to start on Apple Silicon:
  [LANDMINES/postgis-apple-silicon.md](../../LANDMINES/postgis-apple-silicon.md).
- `link_token` prints nothing: the email has not arrived yet. Check
  http://localhost:8025 and run `signin` again.
