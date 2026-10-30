# Build with the repository root as context and --build-arg SERVICE=<service>.
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
RUN --mount=type=cache,target=/go/pkg/mod \
    case "$SERVICE" in \
        user-service|supplier-service|order-service|credit-service|communication-service) ;; \
        *) echo "Unsupported Go SERVICE" >&2; exit 1 ;; \
    esac \
    && cd "$SERVICE" && go mod download

COPY pkg/ ./pkg/
COPY user-service/ ./user-service/
COPY supplier-service/ ./supplier-service/
COPY order-service/ ./order-service/
COPY communication-service/ ./communication-service/
COPY credit-service/ ./credit-service/
COPY docker/k8s/healthcheck/ ./docker/k8s/healthcheck/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd "$SERVICE" \
    && go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
    && go build -trimpath -ldflags="-s -w" -o /out/healthcheck /src/docker/k8s/healthcheck/main.go \
    && if [ "$SERVICE" = order-service ]; then go build -mod=readonly -trimpath -o /out/storage-init ./cmd/storage-init; fi

# Distroless static includes system CA certificates and no compiler or shell.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS runtime
WORKDIR /app
COPY --from=build /out/ /app/
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app/server"]
