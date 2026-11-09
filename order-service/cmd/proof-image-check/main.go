// Command proof-image-check verifies the exact deployment's proof decoders.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/proofimage"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := proofimage.NewDecoder("").CheckCapabilities(ctx); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("PROOF DECODER PASS: JPEG PNG WebP HEIC HEIF actual pixels")
}
