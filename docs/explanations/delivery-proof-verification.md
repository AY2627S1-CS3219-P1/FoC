# Delivery proof verification

These results were observed on application source `d5aa5ee`. Subsequent changes are documentation/artifacts only. No builds or runtime tests were repeated for this cleanup. The mounted proof path targets B3 #179; lifecycle timer startup/enforcement is downstream in #183.

## Observed automated checks

From the repository root:

```sh
make fmt-check && make lint
(cd order-service && GOWORK=off go test -mod=readonly -race ./...)
(cd frontend && npm run check && npm test && npm run build)
```

Observed: zero lint issues, 320 race-enabled Go test passes, 112 frontend tests across 16 files, zero Svelte errors/warnings and a successful production build. Dependency browser-externalization and adapter-auto deployment notices remain; this is not a zero-build-warning claim.

With Docker and ImageMagick/libheif available, run from `order-service/`:

```sh
env -u TEST_DATABASE_URL GOWORK=off go test -mod=readonly -race -tags=integration ./internal/order -run '^Test(PostgresProofReceipts|ProofGrantRefreshPreservesPendingReceipt)$' -count=1 -timeout=5m -v
env -u TEST_DATABASE_URL GOWORK=off go test -mod=readonly -race -tags=integration ./internal/rpc/order -run '^TestProofsThroughConnectPostgresAndS3$' -count=1 -timeout=6m -v
```

Observed: 14 PostgreSQL18 leaf scenarios and two mounted generated-Connect/PostgreSQL/S3/codec scenarios passed. Coverage includes immutable PUT412, cross-replica grant stability, proof verification, on-time pending admission during unavailable inspection, recovery after the deadline, cancellation winning, lost acceptance acknowledgment, identical replay after requester completion, private downloads and one delivery audit/settlement outcome at the original receipt time. Grant rotation during validation no longer invalidates immutable evidence. Invalid image bytes and an uncertified outage do not deliver.

Identity middleware and Credit are scripted external seams. These tests do not establish live login, a live Credit settlement consumer, Kafka/Telegram consumption or production infrastructure. Decoding establishes image validity, not that the photo depicts a delivery.

## Browser fixtures and app artifacts

The existing Svelte route/form and Connect adapter were exercised against explicit synthetic API/storage/auth fixtures without credentials or personal photos. Four scenarios passed at 360/768/1440px with zero page errors: retained failed files, same-identity retry, uncertain removal acknowledgment, denied outage bypass, immutable pending restoration after reload, keyboard retry and participant-only evidence reads.

To repeat, start `npm run dev -- --host 127.0.0.1 --port 5194 --strictPort` from `frontend/` after `npm ci`. From the repository root, with Python Playwright, Pillow 10.1+ and Chromium installed:

```sh
python3 frontend/tests/delivery-proof-ui-e2e.py --url http://127.0.0.1:5194 --out .local/order-proof-browser
```

Use `--browser-channel chrome` for installed Chrome instead. Stop only the frontend started for this check; retain its artifacts. These are repeat instructions, not commands rerun during cleanup. The [desktop/mobile UI verification](delivery-proof-ui/README.md) records the newer 360/390/768/1440px captures, dependency setup and fixed-mobile recording command.

- [Retained failed photos, mobile](delivery-proof-retained-mobile.png)
- [Restored pending confirmation, mobile](delivery-proof-pending-mobile.png)
- [Private gallery, mobile](delivery-proof-gallery-mobile.png)
- [Private gallery, desktop](delivery-proof-gallery-desktop.png)
- [Retry/removal/reload recording](delivery-proof-retry-reload.mp4) ([original WebM](delivery-proof-retry-reload.webm))

Mobile fixture inspection shows dense paragraphs, a truncated native file label, tall verbose status cards, a wrapped retry button and tiny synthetic gallery thumbnails. These views do not establish real-photo quality. No mobile UI was changed in this cleanup.

## Deployed codec boundary

Previously built and exercised from the repository root:

```sh
docker build -f order-service/Dockerfile -t foc-order-proof-local:verification .
docker run --rm --memory=3g --cpus=2 --pids-limit=128 --read-only --tmpfs /tmp:rw,noexec,nosuid,size=64m --entrypoint /proof-image-check foc-order-proof-local:verification
```

The build and restricted nonroot runtime both printed `PROOF DECODER PASS: JPEG PNG WebP HEIC HEIF actual pixels`. The policy restricts coders, delegates, spill and inherited credentials. The container limit is a hard process-group bound, not measured peak RSS. Large-phone regressions separately cover 20MP and the exact 24MP boundary.

## Remaining findings and operational limits

- **N1:** Decoder output-cap failures can appear as retryable unavailability rather than a known rejection. This is code-traced, not separately reproduced at runtime. Such input cannot pass first verification, reserve a receipt or cause false acceptance.
- **N2:** The service adapter imports `DeliveryProofAPI` and `ProofRejectedError` from the feature controller instead of colocating them in the service domain as the frontend convention requires. This placement is not fixed.
- A normally exiting decoder allocation failure can still appear as a precondition rejection; external-kill and timeout handling are retryable.
- Custom-store endpoint identity must stay stable or be deliberately migrated. Cleanup has bounded revisit throughput and a persistent candidate error can delay a batch. Proof reads briefly lock the parent. Probe/recovery replicas may duplicate work or churn certification generations. Runtime dependencies are not digest-pinned; production HA, queue scale and peak RSS are not verified.
- Report/appeal closure-dependent retention and revocation-safe nonparticipant case-admin access remain deferred. Admin role alone grants no proof access.
