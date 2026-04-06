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
		Port         string `yaml:"port"`
		ReadTimeout  string `yaml:"read_timeout"`
		WriteTimeout string `yaml:"write_timeout"`
		APIKey       string `yaml:"api_key"`
		RateLimitRPS int    `yaml:"rate_limit_rps"`
	} `yaml:"server"`
	Database struct {
		SnapshotPath  string `yaml:"snapshot_path"`
		WALPath       string `yaml:"wal_path"`
		SnapshotEvery int    `yaml:"snapshot_every"`
	} `yaml:"database"`
	Limits struct {
		MaxBodyBytes int64 `yaml:"max_body_bytes"`
		MaxVectorDim int   `yaml:"max_vector_dim"`
		MaxK         int   `yaml:"max_k"`
	} `yaml:"limits"`
	Search struct {
		Mode string `yaml:"mode"`
	} `yaml:"search"`
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
	cfg.Server.Port = "19190"
	cfg.Server.ReadTimeout = "10s"
	cfg.Server.WriteTimeout = "10s"
	cfg.Server.APIKey = ""
	cfg.Server.RateLimitRPS = 100
	cfg.Database.SnapshotPath = "./data/snapshot.json"
	cfg.Database.WALPath = "./data/wal.log"
	cfg.Database.SnapshotEvery = 25
	cfg.Limits.MaxBodyBytes = 1 << 20
	cfg.Limits.MaxVectorDim = 4096
	cfg.Limits.MaxK = 100
	cfg.Search.Mode = "exact"
	return cfg
}

func overrideFromEnv(cfg *Config) {
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
	if v := strings.TrimSpace(os.Getenv("VECTOR_DB_RATE_LIMIT_RPS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
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
}
