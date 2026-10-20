// Package proofimage validates actual image bytes with the existing ImageMagick
// decoder rather than implementing media codecs in Order Service.
package proofimage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"time"

	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
)

// Decoder requires a maintained ImageMagick installation with JPEG, PNG, WebP
// and libheif delegates. Deployment wiring must verify those delegates before
// enabling proof uploads. Native codec RSS/CPU/PID limits and a restricted
// ImageMagick policy must also be enforced by deployment; pixel-cache limits
// below are not hard process isolation. No shell, filenames, URLs or caller-selected coder is
// accepted. Invalid media is never classified as a storage outage.
type Decoder struct {
	Executable string
	slots      chan struct{}
}

func NewDecoder(executable string) *Decoder {
	return &Decoder{Executable: executable, slots: make(chan struct{}, 2)}
}

func (d *Decoder) Validate(ctx context.Context, media string, body []byte) error {
	coder := map[string]string{
		"image/jpeg": "jpeg",
		"image/png":  "png",
		"image/webp": "webp",
		"image/heic": "heic",
		"image/heif": "heic",
	}[media]
	if coder == "" || len(body) < 1 || int64(len(body)) > app.MaxImageBytes {
		return app.ErrPrecondition
	}
	if d == nil || d.slots == nil {
		return app.ErrUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case d.slots <- struct{}{}:
		defer func() { <-d.slots }()
	case <-bounded.Done():
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return app.ErrUnavailable
	}
	binary := d.Executable
	if binary == "" {
		binary = "magick"
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		return app.ErrUnavailable
	}
	// Force the chosen decoder, disable spill-to-disk, bound dimensions and
	// memory, and transform pixels to force full decoding rather than identify
	// just the header. Native area admission is strict (<), so 24,000,001
	// admits the exact 24MP boundary. The result is three bytes per frame.
	cmd := exec.CommandContext(bounded, path,
		"-regard-warnings", "-limit", "memory", "1GiB", "-limit", "map", "0",
		"-limit", "disk", "0", "-limit", "width", "8192", "-limit", "height", "8192",
		"-limit", "area", "24000001", "-limit", "thread", "1", "-limit", "time", "5",
		coder+":-", "-resize", "1x1!", "-depth", "8", "rgb:-")
	// Some codecs spool bounded stdin for seeking. Keep that input in a private
	// request directory, distinct from disabled pixel-cache spill, and remove it
	// on every exit. Child codecs do not inherit workload tokens/AWS secrets.
	temporary, err := os.MkdirTemp("", "order-proof-image-")
	if err != nil {
		return app.ErrUnavailable
	}
	defer func() { _ = os.RemoveAll(temporary) }()
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"LANG=C.UTF-8",
		"MAGICK_CONFIGURE_PATH=" + os.Getenv("MAGICK_CONFIGURE_PATH"),
		"MAGICK_TEMPORARY_PATH=" + temporary,
	}
	cmd.Stdin = bytes.NewReader(body)
	output := &boundedOutput{remaining: 1536}
	cmd.Stdout = output
	cmd.Stderr = &boundedOutput{remaining: 2048}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if bounded.Err() != nil {
			return app.ErrUnavailable
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ProcessState == nil || !exit.Exited() {
			// Spawn failures and external kills are unknown validation results,
			// not evidence that immutable, previously verified bytes are bad.
			return app.ErrUnavailable
		}
		return app.ErrPrecondition
	}
	if output.written < 3 || output.written%3 != 0 {
		return app.ErrPrecondition
	}
	return nil
}

type boundedOutput struct{ remaining, written int }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.remaining {
		return 0, errors.New("image decoder output limit exceeded")
	}
	b.remaining -= len(p)
	b.written += len(p)
	return len(p), nil
}

var _ app.ProofImageValidator = (*Decoder)(nil)
