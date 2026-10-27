FROM golang:1.27.0 AS build
WORKDIR /src/order-service
COPY pkg /src/pkg
COPY order-service/go.mod order-service/go.sum ./
RUN GOWORK=off go mod download
COPY order-service ./
RUN CGO_ENABLED=0 GOWORK=off go build -mod=readonly -trimpath -o /order-service ./cmd/server \
 && CGO_ENABLED=0 GOWORK=off go build -mod=readonly -trimpath -o /proof-image-check ./cmd/proof-image-check

FROM debian:trixie-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates imagemagick libheif-plugin-libde265 \
 && rm -rf /var/lib/apt/lists/*
COPY order-service/config/imagemagick/policy.xml /etc/order-proof-imagemagick/policy.xml
ENV MAGICK_CONFIGURE_PATH=/etc/order-proof-imagemagick
COPY --from=build /order-service /order-service
COPY --from=build /proof-image-check /proof-image-check
USER 65532:65532
RUN /proof-image-check
EXPOSE 8080
ENTRYPOINT ["/order-service"]
