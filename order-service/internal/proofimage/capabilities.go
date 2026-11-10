package proofimage

import (
	"context"
	"embed"
	"fmt"
)

//go:embed testdata/valid.jpeg testdata/valid.png testdata/valid.webp testdata/valid.heic
var capabilityImages embed.FS

// CheckCapabilities decodes known actual pixels using the same bounded policy
// as requests. A listed but broken/missing delegate must not enable grants.
func (d *Decoder) CheckCapabilities(ctx context.Context) error {
	for _, format := range []struct{ media, file string }{
		{"image/jpeg", "valid.jpeg"}, {"image/png", "valid.png"}, {"image/webp", "valid.webp"}, {"image/heic", "valid.heic"}, {"image/heif", "valid.heic"},
	} {
		body, err := capabilityImages.ReadFile("testdata/" + format.file)
		if err != nil {
			return err
		}
		if err = d.Validate(ctx, format.media, body); err != nil {
			return fmt.Errorf("%s decoder capability unavailable: %w", format.media, err)
		}
	}
	return nil
}
