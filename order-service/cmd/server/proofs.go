package main

import (
	"context"
	"log/slog"
	"sync"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/images"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/proofimage"
)

type proofRuntime struct {
	proofs   *order.Proofs
	receipts *order.ProofReceipts
	outages  *order.ProofOutages
	cleaner  *order.ProofCleaner
}

// Storage reachability is not a startup gate. Missing local codec capabilities
// disable new grants without taking unrelated reads/mutations or recovery down.
func newProofRuntime(ctx context.Context, repository *order.PostgresRepository, storage *images.S3) *proofRuntime {
	var grants order.ProofGrantIssuer
	var objects order.ProofObjects
	var validator order.ProofImageValidator
	var authority order.ProofOutageAuthority
	scope := "unconfigured"
	outageScope := scope
	runtime := &proofRuntime{}
	if storage != nil {
		scope = storage.ProofScope()
		outageScope = storage.ProofOutageScope()
		objects = storage
		decoder := proofimage.NewDecoder("")
		if err := decoder.CheckCapabilities(ctx); err != nil {
			slog.Error("new delivery proof uploads disabled: local image codec capability check failed", "error", err)
		} else {
			grants = order.NewS3ProofGrants(storage)
			validator = decoder
		}
		runtime.outages = order.NewProofOutages(repository, storage, outageScope)
		authority = runtime.outages
	}
	runtime.proofs = order.NewProofs(
		repository,
		grants,
		objects,
		validator,
		order.ProofOptions{UploadScope: scope, OutageScope: outageScope, Outages: authority},
	)
	runtime.receipts = order.NewProofReceipts(runtime.proofs, repository)
	if storage != nil {
		runtime.cleaner = order.NewProofCleaner(runtime.proofs, repository, storage)
	}
	return runtime
}

func (r *proofRuntime) start(ctx context.Context, workers *sync.WaitGroup) {
	workers.Go(func() { r.receipts.Run(ctx) })
	if r.outages != nil {
		workers.Go(func() { r.outages.Run(ctx) })
	}
	if r.cleaner != nil {
		workers.Go(func() { r.cleaner.Run(ctx) })
	}
}
