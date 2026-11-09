# Proof decoding uses a pinned ImageMagick; see scripts/build-imagemagick.sh.
FROM debian:trixie-slim AS imagemagick
COPY order-service/scripts/imagemagick-deps.sh order-service/scripts/build-imagemagick.sh /scripts/
RUN . /scripts/imagemagick-deps.sh \
 && apt-get update \
 && apt-get install -y --no-install-recommends $IMAGEMAGICK_BUILD_DEPS \
 && sh /scripts/build-imagemagick.sh

FROM golang:1.27.0 AS build
WORKDIR /src/order-service
COPY pkg /src/pkg
COPY order-service/go.mod order-service/go.sum ./
RUN GOWORK=off go mod download
COPY order-service ./
RUN CGO_ENABLED=0 GOWORK=off go build -mod=readonly -trimpath -o /order-service ./cmd/server \
 && CGO_ENABLED=0 GOWORK=off go build -mod=readonly -trimpath -o /proof-image-check ./cmd/proof-image-check

FROM debian:trixie-slim
COPY order-service/scripts/imagemagick-deps.sh /tmp/
RUN . /tmp/imagemagick-deps.sh \
 && apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates $IMAGEMAGICK_RUNTIME_DEPS \
 && rm -rf /var/lib/apt/lists/* /tmp/imagemagick-deps.sh
COPY --from=imagemagick /opt/imagemagick /opt/imagemagick
ENV PATH=/opt/imagemagick/bin:$PATH
COPY order-service/config/imagemagick/policy.xml /etc/order-proof-imagemagick/policy.xml
ENV MAGICK_CONFIGURE_PATH=/etc/order-proof-imagemagick
COPY --from=build /order-service /order-service
COPY --from=build /proof-image-check /proof-image-check
USER 65532:65532
RUN /proof-image-check
EXPOSE 8080
ENTRYPOINT ["/order-service"]
