FROM golang:1.27.0 AS build
WORKDIR /src/moderation-service
COPY pkg /src/pkg
COPY moderation-service/go.mod moderation-service/go.sum ./
RUN GOWORK=off go mod download
COPY moderation-service ./
RUN CGO_ENABLED=0 GOWORK=off go build -mod=readonly -trimpath -o /moderation-service ./cmd/server

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /moderation-service /moderation-service
EXPOSE 8080 8081
USER nonroot:nonroot
ENTRYPOINT ["/moderation-service"]
