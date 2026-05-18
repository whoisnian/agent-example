package messaging

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// testCounter reads the current count of a prometheus.Counter for assertions.
func testCounter(t *testing.T, c prometheus.Counter) int64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatalf("counter write: %v", err)
	}
	return int64(m.GetCounter().GetValue())
}
