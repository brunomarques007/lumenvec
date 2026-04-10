package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaultsWhenFileMissing(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Port != "19190" || cfg.Search.Mode != "exact" {
		t.Fatal("expected default config values")
	}
	if cfg.Server.Protocol != "http" {
		t.Fatal("expected default http protocol")
	}
	if cfg.Search.ANNM != 16 || cfg.Search.ANNEfConstruct != 64 || cfg.Search.ANNEfSearch != 64 {
		t.Fatal("expected default ann config values")
	}
	if cfg.Search.ANNProfile != "balanced" {
		t.Fatal("expected default ann profile")
	}
	if cfg.Search.ANNEvalSampleRate != 0 {
		t.Fatal("expected default ann eval sample rate")
	}
}

func TestLoadFromFileAndEnv(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(`
server:
  protocol: "grpc"
  port: 20000
  read_timeout: 1s
  write_timeout: 2s
  api_key: "from-file"
  rate_limit_rps: 20
database:
  snapshot_path: "./snap.json"
  wal_path: "./wal.log"
  snapshot_every: 12
  vector_store: "disk"
  vector_path: "./vectors"
  cache_enabled: true
  cache_max_bytes: 2048
  cache_max_items: 321
  cache_ttl: "30s"
limits:
  max_body_bytes: 123
  max_vector_dim: 88
  max_k: 9
search:
  mode: "ann"
  ann_profile: "fast"
  ann_m: 24
  ann_ef_construction: 96
  ann_ef_search: 48
  ann_eval_sample_rate: 15
grpc:
  enabled: true
  port: 21000
`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VECTOR_DB_PORT", "30000")
	t.Setenv("VECTOR_DB_API_KEY", "from-env")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Port != "30000" || cfg.Server.APIKey != "from-env" || cfg.Search.Mode != "ann" {
		t.Fatal("expected yaml + env overrides")
	}
	if cfg.Server.Protocol != "grpc" {
		t.Fatal("expected grpc protocol from yaml")
	}
	if cfg.Search.ANNM != 24 || cfg.Search.ANNEfConstruct != 96 || cfg.Search.ANNEfSearch != 48 {
		t.Fatal("expected ann config from yaml")
	}
	if cfg.Search.ANNProfile != "fast" {
		t.Fatal("expected ann profile from yaml")
	}
	if cfg.Search.ANNEvalSampleRate != 15 {
		t.Fatal("expected ann eval sample rate from yaml")
	}
	if !cfg.GRPC.Enabled || cfg.GRPC.Port != "21000" {
		t.Fatal("expected grpc config from yaml")
	}
	if cfg.Database.VectorStore != "disk" || cfg.Database.VectorPath != "./vectors" {
		t.Fatal("expected vector store config from yaml")
	}
	if !cfg.Database.CacheEnabled || cfg.Database.CacheMaxBytes != 2048 || cfg.Database.CacheMaxItems != 321 || cfg.Database.CacheTTL != "30s" {
		t.Fatal("expected cache config from yaml")
	}
}

func TestParseDuration(t *testing.T) {
	if got := ParseDuration("5s", time.Second); got != 5*time.Second {
		t.Fatalf("ParseDuration() = %v", got)
	}
	if got := ParseDuration("bad", time.Second); got != time.Second {
		t.Fatalf("ParseDuration() fallback = %v", got)
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("server: ["), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(cfgPath); err == nil {
		t.Fatal("expected yaml error")
	}
}

func TestOverrideFromEnvIgnoresInvalidNumericValues(t *testing.T) {
	cfg := defaultConfig()
	t.Setenv("VECTOR_DB_RATE_LIMIT_RPS", "bad")
	t.Setenv("VECTOR_DB_SNAPSHOT_EVERY", "-1")
	t.Setenv("VECTOR_DB_MAX_BODY_BYTES", "bad")
	t.Setenv("VECTOR_DB_MAX_VECTOR_DIM", "bad")
	t.Setenv("VECTOR_DB_MAX_K", "bad")
	overrideFromEnv(&cfg)
	if cfg.Server.RateLimitRPS != 100 || cfg.Database.SnapshotEvery != 25 || cfg.Limits.MaxK != 100 {
		t.Fatal("expected invalid env values to be ignored")
	}
}

func TestOverrideFromEnvValidValues(t *testing.T) {
	cfg := defaultConfig()
	t.Setenv("VECTOR_DB_READ_TIMEOUT", "9s")
	t.Setenv("VECTOR_DB_PROTOCOL", "grpc")
	t.Setenv("VECTOR_DB_WRITE_TIMEOUT", "11s")
	t.Setenv("VECTOR_DB_RATE_LIMIT_RPS", "50")
	t.Setenv("VECTOR_DB_SNAPSHOT_PATH", "/tmp/snap")
	t.Setenv("VECTOR_DB_WAL_PATH", "/tmp/wal")
	t.Setenv("VECTOR_DB_SNAPSHOT_EVERY", "30")
	t.Setenv("VECTOR_DB_MAX_BODY_BYTES", "2048")
	t.Setenv("VECTOR_DB_MAX_VECTOR_DIM", "256")
	t.Setenv("VECTOR_DB_MAX_K", "20")
	t.Setenv("VECTOR_DB_SEARCH_MODE", "ann")
	t.Setenv("VECTOR_DB_ANN_PROFILE", "quality")
	t.Setenv("VECTOR_DB_ANN_M", "20")
	t.Setenv("VECTOR_DB_ANN_EF_CONSTRUCTION", "70")
	t.Setenv("VECTOR_DB_ANN_EF_SEARCH", "33")
	t.Setenv("VECTOR_DB_ANN_EVAL_SAMPLE_RATE", "25")
	t.Setenv("VECTOR_DB_CACHE_ENABLED", "true")
	t.Setenv("VECTOR_DB_CACHE_MAX_BYTES", "4096")
	t.Setenv("VECTOR_DB_CACHE_MAX_ITEMS", "222")
	t.Setenv("VECTOR_DB_CACHE_TTL", "45s")
	t.Setenv("VECTOR_DB_VECTOR_STORE", "disk")
	t.Setenv("VECTOR_DB_VECTOR_PATH", "/tmp/vectors")
	t.Setenv("VECTOR_DB_GRPC_ENABLED", "true")
	t.Setenv("VECTOR_DB_GRPC_PORT", "22000")
	overrideFromEnv(&cfg)

	if cfg.Server.Protocol != "grpc" {
		t.Fatal("expected protocol override")
	}
	if cfg.Server.ReadTimeout != "9s" || cfg.Server.WriteTimeout != "11s" {
		t.Fatal("expected timeout overrides")
	}
	if cfg.Server.RateLimitRPS != 50 || cfg.Database.SnapshotEvery != 30 {
		t.Fatal("expected numeric overrides")
	}
	if cfg.Database.SnapshotPath != "/tmp/snap" || cfg.Database.WALPath != "/tmp/wal" {
		t.Fatal("expected path overrides")
	}
	if cfg.Database.VectorStore != "disk" || cfg.Database.VectorPath != "/tmp/vectors" {
		t.Fatal("expected vector store overrides")
	}
	if !cfg.Database.CacheEnabled || cfg.Database.CacheMaxBytes != 4096 || cfg.Database.CacheMaxItems != 222 || cfg.Database.CacheTTL != "45s" {
		t.Fatal("expected cache overrides")
	}
	if cfg.Limits.MaxBodyBytes != 2048 || cfg.Limits.MaxVectorDim != 256 || cfg.Limits.MaxK != 20 {
		t.Fatal("expected limit overrides")
	}
	if cfg.Search.Mode != "ann" {
		t.Fatal("expected search mode override")
	}
	if cfg.Search.ANNM != 20 || cfg.Search.ANNEfConstruct != 70 || cfg.Search.ANNEfSearch != 33 {
		t.Fatal("expected ann overrides")
	}
	if cfg.Search.ANNProfile != "quality" {
		t.Fatal("expected ann profile override")
	}
	if cfg.Search.ANNEvalSampleRate != 25 {
		t.Fatal("expected ann eval sample rate override")
	}
	if !cfg.GRPC.Enabled || cfg.GRPC.Port != "22000" {
		t.Fatal("expected grpc overrides")
	}
}

func TestInvalidProtocolFallsBackToHTTP(t *testing.T) {
	cfg := defaultConfig()
	t.Setenv("VECTOR_DB_PROTOCOL", "invalid")
	overrideFromEnv(&cfg)

	if cfg.Server.Protocol != "http" {
		t.Fatal("expected invalid protocol to fall back to http")
	}
	if cfg.GRPC.Enabled {
		t.Fatal("expected grpc disabled when protocol falls back to http")
	}
}

func TestANNProfileDefaultsAndPartialOverride(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(`
search:
  mode: "ann"
  ann_profile: "fast"
  ann_ef_search: 40
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Search.ANNProfile != "fast" {
		t.Fatal("expected fast ann profile")
	}
	if cfg.Search.ANNM != 8 || cfg.Search.ANNEfConstruct != 32 || cfg.Search.ANNEfSearch != 40 {
		t.Fatal("expected fast profile defaults with explicit ef_search override")
	}
}
