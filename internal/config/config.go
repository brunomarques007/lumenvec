package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server struct {
		Protocol     string `yaml:"protocol"`
		Port         string `yaml:"port"`
		ReadTimeout  string `yaml:"read_timeout"`
		WriteTimeout string `yaml:"write_timeout"`
		APIKey       string `yaml:"api_key"`
		AccessLog    bool   `yaml:"access_log"`
		Metrics      bool   `yaml:"metrics_enabled"`
		RateLimitRPS int    `yaml:"rate_limit_rps"`
	} `yaml:"server"`
	Database struct {
		SnapshotPath  string `yaml:"snapshot_path"`
		WALPath       string `yaml:"wal_path"`
		SnapshotEvery int    `yaml:"snapshot_every"`
		VectorStore   string `yaml:"vector_store"`
		VectorPath    string `yaml:"vector_path"`
		SyncEvery     int    `yaml:"sync_every"`
		CacheEnabled  bool   `yaml:"cache_enabled"`
		CacheMaxBytes int64  `yaml:"cache_max_bytes"`
		CacheMaxItems int    `yaml:"cache_max_items"`
		CacheTTL      string `yaml:"cache_ttl"`
	} `yaml:"database"`
	Limits struct {
		MaxBodyBytes int64 `yaml:"max_body_bytes"`
		MaxVectorDim int   `yaml:"max_vector_dim"`
		MaxK         int   `yaml:"max_k"`
	} `yaml:"limits"`
	Search struct {
		Mode              string `yaml:"mode"`
		ANNProfile        string `yaml:"ann_profile"`
		ANNM              int    `yaml:"ann_m"`
		ANNEfConstruct    int    `yaml:"ann_ef_construction"`
		ANNEfSearch       int    `yaml:"ann_ef_search"`
		ANNEvalSampleRate int    `yaml:"ann_eval_sample_rate"`
	} `yaml:"search"`
	GRPC struct {
		Enabled bool   `yaml:"enabled"`
		Port    string `yaml:"port"`
	} `yaml:"grpc"`
	Security struct {
		Profile string `yaml:"profile"`
		Auth    struct {
			Enabled     bool   `yaml:"enabled"`
			APIKey      string `yaml:"api_key"`
			GRPCEnabled bool   `yaml:"grpc_enabled"`
		} `yaml:"auth"`
		Transport struct {
			TLSEnabled bool   `yaml:"tls_enabled"`
			CertFile   string `yaml:"cert_file"`
			KeyFile    string `yaml:"key_file"`
		} `yaml:"transport"`
		Proxy struct {
			TrustForwardedFor bool     `yaml:"trust_forwarded_for"`
			TrustedProxies    []string `yaml:"trusted_proxies"`
		} `yaml:"proxy"`
		Storage struct {
			StrictFilePermissions bool   `yaml:"strict_file_permissions"`
			DirMode               string `yaml:"dir_mode"`
			FileMode              string `yaml:"file_mode"`
		} `yaml:"storage"`
	} `yaml:"security"`
}

func Load(path string) (Config, error) {
	cfg := defaultConfig()

	data, err := os.ReadFile(path)
	if err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, err
		}
	}

	overrideFromEnv(&cfg)
	return cfg, nil
}

func ParseDuration(raw string, fallback time.Duration) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return d
}

func defaultConfig() Config {
	var cfg Config
	cfg.Server.Protocol = "http"
	cfg.Server.Port = "19190"
	cfg.Server.ReadTimeout = "10s"
	cfg.Server.WriteTimeout = "10s"
	cfg.Server.APIKey = ""
	cfg.Server.AccessLog = false
	cfg.Server.Metrics = true
	cfg.Server.RateLimitRPS = 100
	cfg.Database.SnapshotPath = "./data/snapshot.json"
	cfg.Database.WALPath = "./data/wal.log"
	cfg.Database.SnapshotEvery = 25
	cfg.Database.VectorStore = "memory"
	cfg.Database.VectorPath = "./data/vectors"
	cfg.Database.SyncEvery = 1
	cfg.Database.CacheEnabled = false
	cfg.Database.CacheMaxBytes = 8 << 20
	cfg.Database.CacheMaxItems = 1024
	cfg.Database.CacheTTL = "15m"
	cfg.Limits.MaxBodyBytes = 1 << 20
	cfg.Limits.MaxVectorDim = 4096
	cfg.Limits.MaxK = 100
	cfg.Search.Mode = "exact"
	cfg.Search.ANNProfile = "balanced"
	cfg.Search.ANNEvalSampleRate = 0
	cfg.GRPC.Enabled = false
	cfg.GRPC.Port = "19191"
	cfg.Security.Profile = "development"
	cfg.Security.Auth.Enabled = false
	cfg.Security.Auth.APIKey = ""
	cfg.Security.Auth.GRPCEnabled = false
	cfg.Security.Transport.TLSEnabled = false
	cfg.Security.Transport.CertFile = ""
	cfg.Security.Transport.KeyFile = ""
	cfg.Security.Proxy.TrustForwardedFor = false
	cfg.Security.Proxy.TrustedProxies = nil
	cfg.Security.Storage.StrictFilePermissions = false
	cfg.Security.Storage.DirMode = "0755"
	cfg.Security.Storage.FileMode = "0644"
	return cfg
}

func overrideFromEnv(cfg *Config) {
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_PROTOCOL")); v != "" {
		cfg.Server.Protocol = v
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_PORT")); v != "" {
		cfg.Server.Port = v
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_READ_TIMEOUT")); v != "" {
		cfg.Server.ReadTimeout = v
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_WRITE_TIMEOUT")); v != "" {
		cfg.Server.WriteTimeout = v
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_API_KEY")); v != "" {
		cfg.Server.APIKey = v
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_ACCESS_LOG")); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.Server.AccessLog = enabled
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_METRICS_ENABLED")); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.Server.Metrics = enabled
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_RATE_LIMIT_RPS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.Server.RateLimitRPS = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_SNAPSHOT_PATH")); v != "" {
		cfg.Database.SnapshotPath = v
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_WAL_PATH")); v != "" {
		cfg.Database.WALPath = v
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_SNAPSHOT_EVERY")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Database.SnapshotEvery = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_VECTOR_STORE")); v != "" {
		cfg.Database.VectorStore = v
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_VECTOR_PATH")); v != "" {
		cfg.Database.VectorPath = v
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_SYNC_EVERY")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Database.SyncEvery = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_CACHE_ENABLED")); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.Database.CacheEnabled = enabled
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_CACHE_MAX_BYTES")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.Database.CacheMaxBytes = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_CACHE_MAX_ITEMS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Database.CacheMaxItems = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_CACHE_TTL")); v != "" {
		cfg.Database.CacheTTL = v
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_MAX_BODY_BYTES")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.Limits.MaxBodyBytes = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_MAX_VECTOR_DIM")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Limits.MaxVectorDim = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_MAX_K")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Limits.MaxK = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_SEARCH_MODE")); v != "" {
		cfg.Search.Mode = v
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_ANN_PROFILE")); v != "" {
		cfg.Search.ANNProfile = v
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_ANN_M")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Search.ANNM = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_ANN_EF_CONSTRUCTION")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Search.ANNEfConstruct = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_ANN_EF_SEARCH")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Search.ANNEfSearch = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_ANN_EVAL_SAMPLE_RATE")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.Search.ANNEvalSampleRate = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_GRPC_ENABLED")); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.GRPC.Enabled = enabled
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_GRPC_PORT")); v != "" {
		cfg.GRPC.Port = v
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_SECURITY_PROFILE")); v != "" {
		cfg.Security.Profile = v
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_SECURITY_AUTH_ENABLED")); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.Security.Auth.Enabled = enabled
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_SECURITY_API_KEY")); v != "" {
		cfg.Security.Auth.APIKey = v
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_SECURITY_GRPC_AUTH_ENABLED")); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.Security.Auth.GRPCEnabled = enabled
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_TLS_ENABLED")); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.Security.Transport.TLSEnabled = enabled
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_TLS_CERT_FILE")); v != "" {
		cfg.Security.Transport.CertFile = v
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_TLS_KEY_FILE")); v != "" {
		cfg.Security.Transport.KeyFile = v
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_TRUST_FORWARDED_FOR")); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.Security.Proxy.TrustForwardedFor = enabled
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_TRUSTED_PROXIES")); v != "" {
		parts := strings.Split(v, ",")
		cfg.Security.Proxy.TrustedProxies = cfg.Security.Proxy.TrustedProxies[:0]
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part != "" {
				cfg.Security.Proxy.TrustedProxies = append(cfg.Security.Proxy.TrustedProxies, part)
			}
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_STRICT_FILE_PERMISSIONS")); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.Security.Storage.StrictFilePermissions = enabled
		}
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_STORAGE_DIR_MODE")); v != "" {
		cfg.Security.Storage.DirMode = v
	}
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_STORAGE_FILE_MODE")); v != "" {
		cfg.Security.Storage.FileMode = v
	}
	applyTransportDefaults(cfg)
	applyANNProfileDefaults(cfg)
	applySecurityDefaults(cfg)
}

func applyTransportDefaults(cfg *Config) {
	cfg.Server.Protocol = normalizeProtocol(cfg.Server.Protocol)
	cfg.GRPC.Enabled = cfg.Server.Protocol == "grpc"
}

func normalizeProtocol(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "grpc":
		return "grpc"
	default:
		return "http"
	}
}

func applyANNProfileDefaults(cfg *Config) {
	profile := normalizeANNProfile(cfg.Search.ANNProfile)
	cfg.Search.ANNProfile = profile

	m, efConstruct, efSearch := annProfileDefaults(profile)
	if cfg.Search.ANNM <= 0 {
		cfg.Search.ANNM = m
	}
	if cfg.Search.ANNEfConstruct <= 0 {
		cfg.Search.ANNEfConstruct = efConstruct
	}
	if cfg.Search.ANNEfSearch <= 0 {
		cfg.Search.ANNEfSearch = efSearch
	}
}

func applySecurityDefaults(cfg *Config) {
	profile := normalizeSecurityProfile(cfg.Security.Profile)
	cfg.Security.Profile = profile

	if strings.TrimSpace(cfg.Security.Auth.APIKey) == "" && strings.TrimSpace(cfg.Server.APIKey) != "" {
		cfg.Security.Auth.APIKey = cfg.Server.APIKey
	}

	switch profile {
	case "production":
		if strings.TrimSpace(cfg.Security.Auth.APIKey) != "" {
			cfg.Security.Auth.Enabled = true
		}
		cfg.Security.Auth.GRPCEnabled = cfg.Security.Auth.Enabled
		cfg.Security.Proxy.TrustForwardedFor = cfg.Security.Proxy.TrustForwardedFor && len(cfg.Security.Proxy.TrustedProxies) > 0
		cfg.Security.Storage.StrictFilePermissions = true
		if strings.TrimSpace(cfg.Security.Storage.DirMode) == "" || cfg.Security.Storage.DirMode == "0755" {
			cfg.Security.Storage.DirMode = "0700"
		}
		if strings.TrimSpace(cfg.Security.Storage.FileMode) == "" || cfg.Security.Storage.FileMode == "0644" {
			cfg.Security.Storage.FileMode = "0600"
		}
	default:
		if !cfg.Security.Auth.Enabled {
			cfg.Security.Auth.GRPCEnabled = false
		}
		if strings.TrimSpace(cfg.Security.Storage.DirMode) == "" {
			cfg.Security.Storage.DirMode = "0755"
		}
		if strings.TrimSpace(cfg.Security.Storage.FileMode) == "" {
			cfg.Security.Storage.FileMode = "0644"
		}
	}
}

func normalizeSecurityProfile(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "production":
		return "production"
	default:
		return "development"
	}
}

func normalizeANNProfile(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "fast":
		return "fast"
	case "quality":
		return "quality"
	default:
		return "balanced"
	}
}

func annProfileDefaults(profile string) (m, efConstruct, efSearch int) {
	switch normalizeANNProfile(profile) {
	case "fast":
		return 8, 32, 32
	case "quality":
		return 24, 96, 96
	default:
		return 16, 64, 64
	}
}
