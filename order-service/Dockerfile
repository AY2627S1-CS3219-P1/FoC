FROM golang:1.27.0 AS build
WORKDIR /src/order-service
COPY pkg /src/pkg
COPY order-service/go.mod order-service/go.sum ./
RUN GOWORK=off go mod download
COPY order-service ./
RUN CGO_ENABLED=0 GOWORK=off go build -mod=readonly -trimpath -o /order-service ./cmd/server

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /order-service /order-service
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/order-service"]
