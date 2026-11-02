FROM golang:1.27.2-bookworm@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61 AS build
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
RUN go install github.com/pressly/goose/v3/cmd/goose@v3.28.0

FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
WORKDIR /app
COPY --from=build /go/bin/goose /app/goose
COPY user-service/migrations /migrations/user
COPY supplier-service/database/schema /migrations/supplier
COPY order-service/database/schema /migrations/order
COPY credit-service/database/schema /migrations/credit
COPY communication-service/migrations /migrations/communication
USER 65532:65532
ENTRYPOINT ["/app/goose"]
