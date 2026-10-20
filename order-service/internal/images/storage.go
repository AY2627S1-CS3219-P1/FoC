// Package images defines the provider-independent object storage boundary used
// by future image workflows. It deliberately contains no provider SDK types.
package images

import (
	"context"
	"errors"
	"time"
)

// ErrObjectNotFound distinguishes a missing object from an inspection failure.
// Delete treats a missing object as success.
var ErrObjectNotFound = errors.New("image object not found")

// UploadRequest uses an application-generated stable key as object identity.
// Temporary URLs returned by grants are credentials and must never be stored
// in place of this key.
type UploadRequest struct {
	Key         string
	ContentType string
	SizeBytes   int64
}

// UploadGrant contains the temporary upload URL and the exact request
// requirements supplied by the storage provider.
type UploadGrant struct {
	URL       string
	Method    string
	Headers   map[string]string
	ExpiresAt time.Time
}

// ObjectInfo reports observed metadata. Business limits such as maximum size
// and accepted media types belong to the image workflow, not the provider.
type ObjectInfo struct {
	Exists      bool
	Size        int64
	ContentType string
}

// DownloadGrant is temporary access to an object; persist its stable key
// instead of this URL.
type DownloadGrant struct {
	URL       string
	ExpiresAt time.Time
}

// ObjectStorage is the object store contract for future image workflows.
// Implementations must honor context cancellation. Inspect returns
// ErrObjectNotFound for an absent key and another error for storage failures;
// Delete of an absent key succeeds. Credentials, buckets, and SDK clients stay
// in adapters and configuration, outside domain values.
type ObjectStorage interface {
	CreateUpload(context.Context, UploadRequest) (UploadGrant, error)
	Inspect(context.Context, string) (ObjectInfo, error)
	CreateDownload(context.Context, string) (DownloadGrant, error)
	Delete(context.Context, string) error
}
