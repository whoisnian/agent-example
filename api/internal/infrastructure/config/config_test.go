package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// envMap returns a lookup function backed by an in-memory map (so tests do
// not have to mutate process-level env state).
func envMap(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

// withOSS merges the four required OSS keys into m so tests that aren't about
// OSS can satisfy the required-field validation. Keys already in m win.
func withOSS(m map[string]string) map[string]string {
	out := map[string]string{
		"OSS_ENDPOINT":          "http://oss:9000",
		"OSS_BUCKET":            "artifacts",
		"OSS_ACCESS_KEY_ID":     "akid",
		"OSS_ACCESS_KEY_SECRET": "secret",
	}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func TestLoad_Defaults_FailsWhenRequiredMissing(t *testing.T) {
	_, err := Load("", envMap(map[string]string{}))
	if err == nil {
		t.Fatal("expected error when required keys are missing")
	}
	var mr *MissingRequiredError
	if !errors.As(err, &mr) {
		t.Fatalf("expected MissingRequiredError, got %T: %v", err, err)
	}
	want := map[string]bool{
		"DATABASE_URL": false, "RABBITMQ_URL": false,
		"OSS_ENDPOINT": false, "OSS_BUCKET": false,
		"OSS_ACCESS_KEY_ID": false, "OSS_ACCESS_KEY_SECRET": false,
	}
	for _, k := range mr.Keys {
		if _, ok := want[k]; ok {
			want[k] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("expected %s in missing-required list, got %v", k, mr.Keys)
		}
	}
}

func TestLoad_OSS_RequiredAndDefaults(t *testing.T) {
	// All four shared OSS keys present → loads, and the API-only keys take
	// their documented defaults.
	cfg, err := Load("", envMap(map[string]string{
		"DATABASE_URL":          "postgres://x",
		"RABBITMQ_URL":          "amqp://y",
		"OSS_ENDPOINT":          "http://oss:9000",
		"OSS_BUCKET":            "artifacts",
		"OSS_ACCESS_KEY_ID":     "akid",
		"OSS_ACCESS_KEY_SECRET": "secret",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OSSEndpoint != "http://oss:9000" || cfg.OSSBucket != "artifacts" {
		t.Errorf("OSS endpoint/bucket = %q / %q", cfg.OSSEndpoint, cfg.OSSBucket)
	}
	if cfg.OSSAccessKeyID != "akid" || cfg.OSSAccessKeySecret != "secret" {
		t.Errorf("OSS creds not parsed")
	}
	if cfg.OSSRegion != "us-east-1" {
		t.Errorf("OSSRegion default = %q, want us-east-1", cfg.OSSRegion)
	}
	if !cfg.OSSUsePathStyle {
		t.Errorf("OSSUsePathStyle default = false, want true")
	}
	if cfg.OSSPresignTTL != 5*time.Minute {
		t.Errorf("OSSPresignTTL default = %s, want 5m", cfg.OSSPresignTTL)
	}

	// Missing exactly one shared key → fails to load (fail-fast at boot).
	_, err = Load("", envMap(map[string]string{
		"DATABASE_URL":      "postgres://x",
		"RABBITMQ_URL":      "amqp://y",
		"OSS_ENDPOINT":      "http://oss:9000",
		"OSS_BUCKET":        "artifacts",
		"OSS_ACCESS_KEY_ID": "akid",
		// OSS_ACCESS_KEY_SECRET intentionally absent
	}))
	var mr *MissingRequiredError
	if !errors.As(err, &mr) {
		t.Fatalf("expected MissingRequiredError, got %T: %v", err, err)
	}
}

func TestLoad_EnvOnly_AppliesValues(t *testing.T) {
	cfg, err := Load("", envMap(withOSS(map[string]string{
		"DATABASE_URL":          "postgres://x",
		"RABBITMQ_URL":          "amqp://y",
		"HTTP_ADDR":             ":9090",
		"LOG_LEVEL":             "debug",
		"DB_MAX_CONNS":          "50",
		"SHUTDOWN_DRAIN_TIMEOUT": "10s",
		"DB_MIGRATE_ON_BOOT":    "true",
	})))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DatabaseURL != "postgres://x" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Errorf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q", cfg.LogLevel)
	}
	if cfg.DBMaxConns != 50 {
		t.Errorf("DBMaxConns = %d", cfg.DBMaxConns)
	}
	if cfg.ShutdownDrainTimeout != 10*time.Second {
		t.Errorf("ShutdownDrainTimeout = %s", cfg.ShutdownDrainTimeout)
	}
	if !cfg.DBMigrateOnBoot {
		t.Errorf("DBMigrateOnBoot = false")
	}
}

func TestLoad_YAMLOverlay_Then_EnvWins(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	yaml := `
database_url: postgres://from-yaml
rabbitmq_url: amqp://from-yaml
oss_endpoint: http://oss:9000
oss_bucket: artifacts
oss_access_key_id: akid
oss_access_key_secret: secret
http_addr: ":7000"
log_level: warn
db_max_conns: 33
`
	if err := os.WriteFile(yamlPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	// Env overrides yaml for HTTP_ADDR; required keys come from yaml only.
	cfg, err := Load(yamlPath, envMap(map[string]string{
		"HTTP_ADDR": ":9999",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DatabaseURL != "postgres://from-yaml" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.HTTPAddr != ":9999" {
		t.Errorf("expected env override :9999, got %q", cfg.HTTPAddr)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel = %q", cfg.LogLevel)
	}
	if cfg.DBMaxConns != 33 {
		t.Errorf("DBMaxConns = %d", cfg.DBMaxConns)
	}
}

func TestLoad_BadYAMLPath_Errors(t *testing.T) {
	_, err := Load("/nonexistent/path.yaml", envMap(map[string]string{
		"DATABASE_URL": "x",
		"RABBITMQ_URL": "y",
	}))
	if err == nil {
		t.Fatal("expected error for missing yaml file")
	}
}

func TestLoad_EventConsumerPrefetch_DefaultAndOverride(t *testing.T) {
	cfg, err := Load("", envMap(withOSS(map[string]string{
		"DATABASE_URL": "postgres://x",
		"RABBITMQ_URL": "amqp://y",
	})))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.EventConsumerPrefetch != 16 {
		t.Errorf("EventConsumerPrefetch default = %d, want 16", cfg.EventConsumerPrefetch)
	}

	cfg, err = Load("", envMap(withOSS(map[string]string{
		"DATABASE_URL":            "postgres://x",
		"RABBITMQ_URL":            "amqp://y",
		"EVENT_CONSUMER_PREFETCH": "64",
	})))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.EventConsumerPrefetch != 64 {
		t.Errorf("EventConsumerPrefetch override = %d, want 64", cfg.EventConsumerPrefetch)
	}
}
