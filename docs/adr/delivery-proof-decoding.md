# Decode delivery-proof images with ImageMagick and libheif

Use a maintained ImageMagick installation with JPEG, PNG, WebP and libheif delegates to validate actual proof pixels outside the Order transaction. The accepted media types include HEIC and HEIF, which Go's standard image packages do not decode. Metadata inspection alone cannot reject a text file labelled JPEG. Implementing our own codecs would add parser maintenance and security obligations without advancing the delivery workflow.

This decision applies to the mounted proof runtime. Production enablement requires the deployment guarantees below. The production image installs the codecs and checks all five formats under its restricted policy before enabling grants. The validator establishes that the bytes decode as the declared image type, not that the photograph shows a delivery.

## Alternatives

- Metadata-only inspection reuses the original image path but does not meet the actual-image validation requirement.
- Go's standard codecs avoid a native subprocess but do not cover every required format. A separate HEIF binding or decoder service would still introduce native-code deployment or another service dependency.
- ImageMagick/libheif supports the required formats in one maintained toolchain, at the cost of explicit codec and process isolation requirements.

## Operational requirements

Force the allowlisted input coder and feed bytes through stdin, not caller-controlled filenames, URLs or shell commands. Decode pixels rather than only identify a header. Bound file bytes to 10,000,000, native execution to five seconds, dimensions to 8192 per axis, output size and concurrent work. Two application verification slots must be acquired before object allocation. One shared decoder instance has two subprocess slots and one native thread per invocation.

Use a 1 GiB pixel-cache ceiling with memory mapping and disk spill disabled. The previous 128 MiB setting could reject ordinary 20–24MP JPEGs despite their compressed size satisfying the byte contract. Native area admission is strict, so an area threshold of 24,000,001 includes the exact 24MP boundary. Regression fixtures independently generate 20MP and 24MP JPEGs. This larger cache allowance is not an allocation reservation or a hard process-RSS limit. Native decoder allocations can exceed cache accounting. The deployment must provision the two simultaneous processes, apply memory/CPU/PID limits and a restricted ImageMagick policy, and verify format support on its exact ImageMagick/quantum-depth build before enabling upload grants. No production RSS measurement or isolated decoder deployment is claimed here.

An execution timeout is a retryable server validation failure, not evidence of an invalid image or a storage outage. Malformed media remains a precondition rejection. CI/deployment capability verification must exercise all required format cases without skips. Missing runtime support must not allow the system to issue grants for photos it cannot validate later.

## Evidence lifetime and authorization

Explicit photo removal must retire its scoped upload identity before the browser discards it. Retirement releases the active staging slot and atomically leaves a fence even if the original admission response was lost. Late signing/admission and confirmation cannot revive or attach that identity. Accepted evidence cannot be retired. The concrete persistence/cleanup adapter keeps the identity fence, preserves outstanding grant expiry and deletes abandoned bytes only after those grants expire. Retired keys are periodically revisited to reap a PUT that finished after an earlier deletion. Retirement does not revoke an already issued PUT URL. The active staging cap is not a total abandoned-object or request-rate quota; deployment still needs authenticated admission rate and storage controls.

Nonparticipant admin proof reads stay unavailable. A remote boolean case-assignment check is not revocation-safe between lookup and download signing. Reintroducing case-admin access requires an authoritative authorization design that defines revocation and signed-link lifetime together. Current requester/courier access is unchanged, and automated retention remains separately deferred.
