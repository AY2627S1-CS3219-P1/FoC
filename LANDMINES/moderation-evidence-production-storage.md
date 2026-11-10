# Production moderation evidence uploads are unavailable

**Applies when:** you deploy `moderation-service` with the AWS or production overlay, or you test report, appeal or response image uploads outside the local stack.

**Symptom:** reports, appeals and responses can be submitted, but adding an evidence image fails with "Image uploads aren't available. You can still submit without images." `CreateUpload` returns `UPLOAD_UNAVAILABLE`, and the service logs `MODERATION_S3_BUCKET is not set; evidence uploads are unavailable` at startup.

**Cause:** only the local overlay (`infra/k8s/overlays/local/dependencies/kustomization.yaml`) sets `MODERATION_S3_BUCKET` and its endpoints. Terraform provisions an image bucket and a workload role for Order Service only. With no bucket configured, `newEvidenceStorage` in `moderation-service/cmd/server/services.go` disables uploads.

**Current workaround:** none in deployed environments. Use the local Compose or Kubernetes stack to exercise evidence uploads.

**Remove this entry when:** #357 lands. That means Terraform provisions the moderation bucket and IRSA role, the AWS overlay sets the bucket configuration and ServiceAccount annotation, and an upload and a download succeed on a deployed release.
