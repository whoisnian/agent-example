package httpapi

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Probe runs a single readiness check. It MUST return promptly (within the
// timeout configured by the registry) and MUST NOT block on long I/O.
type Probe func(ctx context.Context) error

// ProbeRegistry collects named probes and exposes them as `GET /readyz`.
// Probes are run sequentially with a per-probe timeout; the aggregate response
// lists every failing probe.
type ProbeRegistry struct {
	mu      sync.RWMutex
	probes  map[string]Probe
	timeout time.Duration
}

// NewProbeRegistry builds an empty registry. `timeout` is the per-probe
// deadline; pass <=0 to use the default of 1s.
func NewProbeRegistry(timeout time.Duration) *ProbeRegistry {
	if timeout <= 0 {
		timeout = time.Second
	}
	return &ProbeRegistry{
		probes:  make(map[string]Probe),
		timeout: timeout,
	}
}

// Register stores a probe under the given name. Re-registering replaces the
// existing probe (useful for tests).
func (r *ProbeRegistry) Register(name string, p Probe) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.probes[name] = p
}

// Snapshot returns a copy of the probe map. Used by tests / handlers.
func (r *ProbeRegistry) Snapshot() map[string]Probe {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Probe, len(r.probes))
	for k, v := range r.probes {
		out[k] = v
	}
	return out
}

// healthzHandler always returns 200; only the process being unable to handle
// any request would prevent this from responding (and at that point the
// liveness probe in k8s does the right thing).
func healthzHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

// readyzHandler runs every registered probe; a 503 is returned if any fails,
// with `failed` listing the names. A 200 is returned when all pass (including
// the trivial case of zero probes).
func readyzHandler(reg *ProbeRegistry) gin.HandlerFunc {
	return func(c *gin.Context) {
		probes := reg.Snapshot()
		failed := make([]string, 0)
		details := make(map[string]string, len(probes))

		for name, probe := range probes {
			ctx, cancel := context.WithTimeout(c.Request.Context(), reg.timeout)
			err := probe(ctx)
			cancel()
			if err != nil {
				failed = append(failed, name)
				details[name] = err.Error()
			}
		}

		body := gin.H{
			"status":  "ok",
			"failed":  failed,
			"details": details,
		}
		status := http.StatusOK
		if len(failed) > 0 {
			body["status"] = "unavailable"
			status = http.StatusServiceUnavailable
		}
		c.JSON(status, body)
	}
}
