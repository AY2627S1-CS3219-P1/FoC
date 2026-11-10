FROM golang:1.27.0 AS build
WORKDIR /src/credit-service
COPY pkg /src/pkg
COPY credit-service/go.mod credit-service/go.sum ./
RUN GOWORK=off go mod download
COPY credit-service ./
RUN CGO_ENABLED=0 GOWORK=off go build -mod=readonly -trimpath -o /credit-service ./cmd/server

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /credit-service /credit-service
EXPOSE 8080 8081
USER nonroot:nonroot
ENTRYPOINT ["/credit-service"]
