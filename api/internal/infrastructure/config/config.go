// Package config loads service configuration from env vars (12-factor) with an
// optional YAML overlay. Env wins over YAML. Required keys missing at startup
// cause the loader to return an error naming each missing key.
package config

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all runtime configuration for the API service.
//
// Field tags:
//   - env:"NAME"           — environment variable to read
//   - envDefault:"value"   — default when env is unset
//   - required:"true"      — missing value causes a load failure
//   - yaml:"key"           — YAML key (lowercase env-name by convention)
type Config struct {
	// HTTP
	HTTPAddr             string        `env:"HTTP_ADDR" envDefault:":8080" yaml:"http_addr"`
	ShutdownDrainTimeout time.Duration `env:"SHUTDOWN_DRAIN_TIMEOUT" envDefault:"30s" yaml:"shutdown_drain_timeout"`

	// Logging
	LogLevel string `env:"LOG_LEVEL" envDefault:"info" yaml:"log_level"`

	// Database
	DatabaseURL        string        `env:"DATABASE_URL" required:"true" yaml:"database_url"`
	DBMaxConns         int32         `env:"DB_MAX_CONNS" envDefault:"20" yaml:"db_max_conns"`
	DBMinConns         int32         `env:"DB_MIN_CONNS" envDefault:"2" yaml:"db_min_conns"`
	DBMaxConnLifetime  time.Duration `env:"DB_MAX_CONN_LIFETIME" envDefault:"30m" yaml:"db_max_conn_lifetime"`
	DBMaxConnIdleTime  time.Duration `env:"DB_MAX_CONN_IDLE_TIME" envDefault:"5m" yaml:"db_max_conn_idle_time"`
	DBMigrateOnBoot    bool          `env:"DB_MIGRATE_ON_BOOT" envDefault:"false" yaml:"db_migrate_on_boot"`
	DBConnectTimeout   time.Duration `env:"DB_CONNECT_TIMEOUT" envDefault:"5s" yaml:"db_connect_timeout"`

	// RabbitMQ
	RabbitMQURL            string        `env:"RABBITMQ_URL" required:"true" yaml:"rabbitmq_url"`
	RabbitMQConfirmTimeout time.Duration `env:"RABBITMQ_CONFIRM_TIMEOUT" envDefault:"5s" yaml:"rabbitmq_confirm_timeout"`

	// Outbox Relayer
	OutboxBatchSize   int           `env:"OUTBOX_BATCH_SIZE" envDefault:"100" yaml:"outbox_batch_size"`
	OutboxTickInterval time.Duration `env:"OUTBOX_TICK_INTERVAL" envDefault:"1s" yaml:"outbox_tick_interval"`
	OutboxMaxAttempts int           `env:"OUTBOX_MAX_ATTEMPTS" envDefault:"10" yaml:"outbox_max_attempts"`
	OutboxLockID      int64         `env:"OUTBOX_LOCK_ID" envDefault:"918273645" yaml:"outbox_lock_id"`

	// Event-ingest consumer (add-event-ingest-status-sync)
	EventConsumerPrefetch int `env:"EVENT_CONSUMER_PREFETCH" envDefault:"16" yaml:"event_consumer_prefetch"`

	// Cost-ingest consumer (add-cost-service)
	CostIngestPrefetch int `env:"COST_INGEST_PREFETCH" envDefault:"64" yaml:"cost_ingest_prefetch"`

	// Observability
	OTLPEndpoint string `env:"OTEL_EXPORTER_OTLP_ENDPOINT" envDefault:"" yaml:"otlp_endpoint"`
	ServiceName  string `env:"OTEL_SERVICE_NAME" envDefault:"api" yaml:"service_name"`

	// Task-write-api
	DefaultLane         string        `env:"DEFAULT_LANE" envDefault:"default" yaml:"default_lane"`
	DefaultTaskDeadline time.Duration `env:"DEFAULT_TASK_DEADLINE" envDefault:"60m" yaml:"default_task_deadline"`
	// Dev-mode principal — auth middleware is a stub today; these IDs fill in
	// the tenant/user fields the schema requires until JWT extraction lands.
	DevTenantID string `env:"DEV_TENANT_ID" envDefault:"00000000-0000-0000-0000-000000000001" yaml:"dev_tenant_id"`
	DevUserID   string `env:"DEV_USER_ID" envDefault:"00000000-0000-0000-0000-000000000002" yaml:"dev_user_id"`

	// OSS / artifacts-api. The four credential/location keys REUSE the worker's
	// exact env names (worker/worker/core/config.py) so a single shared config
	// drives both processes — note OSS_ACCESS_KEY_SECRET, not the AWS-idiomatic
	// OSS_SECRET_ACCESS_KEY. They are required:"true" (same fail-fast path as
	// DATABASE_URL): the API refuses to boot without them rather than returning
	// a per-request error. The remaining keys are API-only. Credentials MUST NOT
	// be logged; the loader has no config-dump line, and if one is ever added it
	// must redact OSSAccessKeyID / OSSAccessKeySecret.
	OSSEndpoint        string        `env:"OSS_ENDPOINT" required:"true" yaml:"oss_endpoint"`
	OSSBucket          string        `env:"OSS_BUCKET" required:"true" yaml:"oss_bucket"`
	OSSAccessKeyID     string        `env:"OSS_ACCESS_KEY_ID" required:"true" yaml:"oss_access_key_id"`
	OSSAccessKeySecret string        `env:"OSS_ACCESS_KEY_SECRET" required:"true" yaml:"oss_access_key_secret"`
	OSSRegion          string        `env:"OSS_REGION" envDefault:"us-east-1" yaml:"oss_region"`
	OSSUsePathStyle    bool          `env:"OSS_USE_PATH_STYLE" envDefault:"true" yaml:"oss_use_path_style"`
	OSSPresignTTL      time.Duration `env:"OSS_PRESIGN_TTL" envDefault:"5m" yaml:"oss_presign_ttl"`
}

// Load builds Config by:
//  1. starting from struct defaults,
//  2. overlaying values from the YAML file at `yamlPath` (if non-empty),
//  3. overlaying values from environment variables (env wins),
//  4. validating required fields.
//
// `lookupEnv` is the env lookup func; pass os.LookupEnv in production.
func Load(yamlPath string, lookupEnv func(string) (string, bool)) (*Config, error) {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	cfg := &Config{}
	if err := applyDefaults(cfg); err != nil {
		return nil, fmt.Errorf("apply defaults: %w", err)
	}
	if yamlPath != "" {
		if err := applyYAML(cfg, yamlPath); err != nil {
			return nil, fmt.Errorf("apply yaml overlay: %w", err)
		}
	}
	if err := applyEnv(cfg, lookupEnv); err != nil {
		return nil, fmt.Errorf("apply env: %w", err)
	}
	if err := validate(cfg, lookupEnv); err != nil {
		return nil, err
	}
	return cfg, nil
}

// applyDefaults walks struct fields and sets values from `envDefault` tags.
func applyDefaults(cfg *Config) error {
	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		def, ok := t.Field(i).Tag.Lookup("envDefault")
		if !ok || def == "" {
			continue
		}
		if err := setField(v.Field(i), def); err != nil {
			return fmt.Errorf("default for %s: %w", t.Field(i).Name, err)
		}
	}
	return nil
}

// applyYAML parses the file and overlays present keys onto cfg.
func applyYAML(cfg *Config, path string) error {
	raw, err := os.ReadFile(path) //nolint:gosec // path is operator-supplied
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("parse yaml %s: %w", path, err)
	}
	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		key := t.Field(i).Tag.Get("yaml")
		if key == "" {
			continue
		}
		raw, ok := m[key]
		if !ok {
			continue
		}
		s := fmt.Sprintf("%v", raw)
		if err := setField(v.Field(i), s); err != nil {
			return fmt.Errorf("yaml key %s: %w", key, err)
		}
	}
	return nil
}

// applyEnv overlays env values onto cfg using each field's `env` tag.
func applyEnv(cfg *Config, lookup func(string) (string, bool)) error {
	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		key := t.Field(i).Tag.Get("env")
		if key == "" {
			continue
		}
		val, ok := lookup(key)
		if !ok {
			continue
		}
		if err := setField(v.Field(i), val); err != nil {
			return fmt.Errorf("env %s: %w", key, err)
		}
	}
	return nil
}

// validate ensures every `required:"true"` field has a non-zero / non-empty value.
// The check is performed against the resolved Config (after env/YAML overlay) so
// that defaults and YAML can satisfy a required field even if no env is set.
func validate(cfg *Config, lookup func(string) (string, bool)) error {
	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()
	var missing []string
	for i := 0; i < v.NumField(); i++ {
		req := t.Field(i).Tag.Get("required")
		if req != "true" {
			continue
		}
		if isZero(v.Field(i)) {
			missing = append(missing, t.Field(i).Tag.Get("env"))
		}
	}
	if len(missing) > 0 {
		return &MissingRequiredError{Keys: missing}
	}
	_ = lookup
	return nil
}

// setField parses `value` and writes it into the reflect.Value of supported kinds.
func setField(f reflect.Value, value string) error {
	switch f.Kind() {
	case reflect.String:
		f.SetString(value)
	case reflect.Bool:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		f.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// time.Duration also lands here (Kind == Int64); detect via Type.
		if f.Type() == reflect.TypeOf(time.Duration(0)) {
			d, err := time.ParseDuration(value)
			if err != nil {
				return err
			}
			f.SetInt(int64(d))
			return nil
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
		f.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return err
		}
		f.SetUint(n)
	case reflect.Float32, reflect.Float64:
		fl, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return err
		}
		f.SetFloat(fl)
	default:
		return fmt.Errorf("unsupported kind %s", f.Kind())
	}
	return nil
}

// isZero returns true when the field value is the type's zero value.
func isZero(f reflect.Value) bool {
	switch f.Kind() {
	case reflect.String:
		return strings.TrimSpace(f.String()) == ""
	default:
		return f.IsZero()
	}
}

// MissingRequiredError is returned by Load when one or more required keys are unset.
type MissingRequiredError struct {
	Keys []string
}

func (e *MissingRequiredError) Error() string {
	return "missing required configuration keys: " + strings.Join(e.Keys, ", ")
}

// IsMissingRequired reports whether err is a MissingRequiredError.
func IsMissingRequired(err error) bool {
	var target *MissingRequiredError
	return errors.As(err, &target)
}
