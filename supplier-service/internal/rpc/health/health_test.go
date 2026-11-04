package health

import (
	"connectrpc.com/connect"
	"context"
	supplierv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1"
	"testing"
)

func TestHealthReportsStatusWithoutDependencies(t *testing.T) {
	response, err := NewServer().Check(context.Background(), connect.NewRequest(&supplierv1.CheckRequest{}))
	if err != nil || response.Msg.Status != "ok" {
		t.Fatalf("response=%v err=%v", response, err)
	}
}
