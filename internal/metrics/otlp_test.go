package metrics

import (
	"context"
	"testing"
)

// TestStart_NoSchemaURLConflict guards against a real bug hit on
// lumo-movies-svc: resource.Merge(resource.Default(), ...) failing with
// "conflicting Schema URL" because the semconv import didn't match the
// SDK's own internal semconv version. The resource merge happens
// synchronously in Start(), before any real network call, so a fake
// endpoint is enough to catch it.
func TestStart_NoSchemaURLConflict(t *testing.T) {
	shutdown, err := Start(context.Background(), true, "http://127.0.0.1:0/v1/metrics", "Basic fake")
	if err != nil {
		t.Fatalf("Start() returned an error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Start() returned a nil shutdown func")
	}
	_ = shutdown(context.Background())
}

func TestStart_DisabledIsNoop(t *testing.T) {
	shutdown, err := Start(context.Background(), false, "", "")
	if err != nil {
		t.Fatalf("Start() returned an error: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown() returned an error: %v", err)
	}
}
