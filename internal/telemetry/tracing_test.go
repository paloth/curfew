package telemetry

import (
	"context"
	"testing"
)

// Catches resource setup errors, such as a semconv schema URL conflict after an otel upgrade.
func TestInitTracer(t *testing.T) {
	shutdown, err := InitTracer("curfew", "test", "localhost:4318")
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
