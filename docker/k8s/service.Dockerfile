# Build with the repository root as context and --build-arg SERVICE=<service>.
ARG SERVICE=user-service
FROM golang:1.27.2-bookworm@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61 AS build
ARG SERVICE
ENV CGO_ENABLED=0 \
    GOWORK=off \
    GOTOOLCHAIN=local
WORKDIR /src

# Keep sibling modules available for the checked-in local replacements.
COPY pkg/go.mod pkg/go.sum ./pkg/
COPY user-service/go.mod user-service/go.sum ./user-service/
COPY supplier-service/go.mod supplier-service/go.sum ./supplier-service/
COPY order-service/go.mod order-service/go.sum ./order-service/
COPY communication-service/go.mod communication-service/go.sum ./communication-service/
COPY credit-service/go.mod credit-service/go.sum ./credit-service/
COPY content-moderation-service/go.mod content-moderation-service/go.sum ./content-moderation-service/
RUN --mount=type=cache,target=/go/pkg/mod \
    case "$SERVICE" in \
        user-service|supplier-service|order-service|credit-service|communication-service|content-moderation-service) ;; \
        *) echo "Unsupported Go SERVICE" >&2; exit 1 ;; \
    esac \
    && cd "$SERVICE" && go mod download

COPY pkg/ ./pkg/
COPY user-service/ ./user-service/
COPY supplier-service/ ./supplier-service/
COPY order-service/ ./order-service/
COPY communication-service/ ./communication-service/
COPY credit-service/ ./credit-service/
COPY content-moderation-service/ ./content-moderation-service/
COPY docker/k8s/healthcheck/ ./docker/k8s/healthcheck/
RUN --mount=type=cache,target=/root/.cache/go-build \
    GO111MODULE=off go test ./docker/k8s/healthcheck
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd "$SERVICE" \
    && go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
    && go build -trimpath -ldflags="-s -w" -o /out/healthcheck /src/docker/k8s/healthcheck/main.go \
    && if [ "$SERVICE" = order-service ] || [ "$SERVICE" = user-service ]; then \
         go build -mod=readonly -trimpath -o /out/storage-init ./cmd/storage-init; \
       fi \
    && if [ "$SERVICE" = order-service ]; then \
         go build -mod=readonly -trimpath -o /out/proof-image-check ./cmd/proof-image-check; \
       fi

# Distroless static includes system CA certificates and no compiler or shell.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS runtime
WORKDIR /app
COPY --from=build /out/ /app/
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app/server"]

# Build the maintained upstream decoder with the delegates used by the proof
# contract. Debian's current ImageMagick backport makes canonical path denies
# sticky, which rejects the decoder's bounded private stdin spool files.
FROM debian:trixie-slim@sha256:f6b08b09a05725f9431502d3b5714f5c4576e82c1a84fa1debe0c24a7e0c3ec4 AS proof-codecs
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates build-essential pkg-config xz-utils \
         libde265-dev libheif-dev libjpeg-dev libpng-dev libwebp-dev \
    && rm -rf /var/lib/apt/lists/*
ADD --checksum=sha256:9554556c8cbf7d347cbd6bedae931ccf323a2bd5e0f08d4834856568ee19f080 \
    https://github.com/ImageMagick/ImageMagick/releases/download/7.1.2-35/ImageMagick-7.1.2-35.tar.xz /tmp/imagemagick.tar.xz
RUN tar -xJf /tmp/imagemagick.tar.xz -C /tmp \
    && cd /tmp/ImageMagick-7.1.2-35 \
    && ./configure --prefix=/opt/imagemagick --disable-shared --disable-openmp \
         --without-modules --without-x --without-magick-plus-plus --without-perl \
         --with-heic --with-jpeg --with-png --with-webp \
    && make -j2 \
    && make install

# Only Order needs native image codecs; other services stay distroless.
FROM debian:trixie-slim@sha256:f6b08b09a05725f9431502d3b5714f5c4576e82c1a84fa1debe0c24a7e0c3ec4 AS order-service-runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates libheif1 libheif-plugin-libde265 \
         libjpeg62-turbo libpng16-16t64 libwebp7 libwebpdemux2 libwebpmux3 \
    && rm -rf /var/lib/apt/lists/*
COPY --from=proof-codecs /opt/imagemagick /opt/imagemagick
WORKDIR /app
COPY --from=build /out/ /app/
COPY order-service/config/imagemagick/policy.xml /etc/order-proof-imagemagick/policy.xml
ENV PATH=/opt/imagemagick/bin:$PATH \
    MAGICK_CONFIGURE_PATH=/etc/order-proof-imagemagick
USER 65532:65532
RUN /app/proof-image-check
EXPOSE 8080
ENTRYPOINT ["/app/server"]

FROM runtime AS user-service-runtime
FROM runtime AS supplier-service-runtime
FROM runtime AS credit-service-runtime
FROM runtime AS communication-service-runtime
FROM runtime AS content-moderation-service-runtime

# Select by SERVICE for every caller, including direct docker builds.
FROM ${SERVICE}-runtime AS final
