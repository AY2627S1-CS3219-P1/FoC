module github.com/AY2627S1-CS3219-P1/FoC/user-service

go 1.27.0

require (
	connectrpc.com/connect v1.21.0
	github.com/AY2627S1-CS3219-P1/FoC/pkg v0.0.0
	github.com/MicahParks/jwkset v0.11.3
	github.com/MicahParks/keyfunc/v3 v3.8.2
	github.com/go-chi/chi/v5 v5.2.3
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/jackc/pgx/v5 v5.7.6
	github.com/joho/godotenv v1.5.1
	github.com/rs/cors v1.11.1
)

require (
	github.com/gabriel-vasile/mimetype v1.4.10 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.28.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	golang.org/x/crypto v0.45.0 // indirect
	golang.org/x/sync v0.18.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/text v0.31.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace github.com/AY2627S1-CS3219-P1/FoC/pkg => ../pkg
