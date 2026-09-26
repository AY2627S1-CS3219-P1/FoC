FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/user-service ./cmd/server

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/user-service /user-service
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/user-service"]
