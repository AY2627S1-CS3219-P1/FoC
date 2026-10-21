# Delivery proof verification

These historical results were observed on application source `d5aa5ee`. The original documentation/artifact cleanup did not repeat builds or runtime tests. [Later UI checks](delivery-proof-ui/README.md#later-copy-and-control-cleanup) record the copy, control and camera changes separately. The mounted proof path targets B3 #179; lifecycle timer startup/enforcement is downstream in #183.

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

- [Retained failed photos, mobile](https://github.com/user-attachments/assets/9548a782-cae0-4a66-96f6-b83ec48a2fac)
- [Restored pending confirmation, mobile](https://github.com/user-attachments/assets/aa58e75d-cec6-489f-ae14-862207ca865b)
- [Private gallery, mobile](https://github.com/user-attachments/assets/388b55a6-f011-464b-86e7-236e97cb48c6)
- [Private gallery, desktop](https://github.com/user-attachments/assets/ab9d971c-9c49-4eac-bec9-9c536ba95bdb)
- [Retry/removal/reload recording](https://github.com/user-attachments/assets/d172adf9-4cb0-452c-9607-d9b412357c20) ([original WebM](https://github.com/user-attachments/assets/b5add7b1-63fc-4936-8a94-a1c72d5fef72))

Mobile fixture inspection shows dense paragraphs, a truncated native file label, tall verbose status cards, a wrapped retry button and tiny synthetic gallery thumbnails. These views do not establish real-photo quality. No mobile UI was changed in this cleanup.

## Deployed codec boundary

Previously built and exercised from the repository root:

```sh
docker build -f order-service/Dockerfile -t foc-order-proof-local:verification .
docker run --rm --memory=3g --cpus=2 --pids-limit=128 --read-only --tmpfs /tmp:rw,noexec,nosuid,size=64m --entrypoint /proof-image-check foc-order-proof-local:verification
```

The build and restricted nonroot runtime both printed `PROOF DECODER PASS: JPEG PNG WebP HEIC HEIF actual pixels`. The policy restricts coders, delegates, spill and inherited credentials. The container limit is a hard process-group bound, not measured peak RSS. Large-phone regressions separately cover 20MP and the exact 24MP boundary.

## Remaining findings and operational limits

- Decoder RGB output beyond its cap is a precondition rejection. A subprocess regression covers the writer-error path; execution timeouts and external kills remain retryable.
- `DeliveryProofAPI`, upload grants and authoritative rejection errors live in the delivery-proof service domain. The adapter and UI controller import that shared contract.
- A normally exiting decoder allocation failure can still appear as a precondition rejection; external-kill and timeout handling are retryable.
- Custom-store endpoint identity must stay stable or be deliberately migrated. Cleanup has bounded revisit throughput and a persistent candidate error can delay a batch. Proof reads briefly lock the parent. Probe/recovery replicas may duplicate work or churn certification generations. Runtime dependencies are not digest-pinned; production HA, queue scale and peak RSS are not verified.
- Report/appeal closure-dependent retention and revocation-safe nonparticipant case-admin access remain deferred. Admin role alone grants no proof access.
