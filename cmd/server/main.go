package main

import (
	"flag"
	"fmt"
	"log"
	"lumenvec/internal/api"
	"lumenvec/internal/config"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	executeFunc = execute
	logFatalf   = log.Fatalf
	logInfof    = log.Println
)

func main() {
	mustExecute(executeFunc, resolveConfigPath(), runServer)
	logInfof("Server stopped")
}

func execute(configPath string, runner func(serverRunner)) error {
	server, err := buildServer(configPath)
	if err != nil {
		return err
	}
	runner(server)
	return nil
}

func buildServer(configPath string) (*api.Server, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	server := api.NewServerWithOptions(api.ServerOptions{
		Protocol:          cfg.Server.Protocol,
		Port:              cfg.Server.Port,
		ReadTimeout:       config.ParseDuration(cfg.Server.ReadTimeout, 10*time.Second),
		WriteTimeout:      config.ParseDuration(cfg.Server.WriteTimeout, 10*time.Second),
		MaxBodyBytes:      cfg.Limits.MaxBodyBytes,
		MaxVectorDim:      cfg.Limits.MaxVectorDim,
		MaxK:              cfg.Limits.MaxK,
		SnapshotPath:      cfg.Database.SnapshotPath,
		WALPath:           cfg.Database.WALPath,
		SnapshotEvery:     cfg.Database.SnapshotEvery,
		VectorStore:       cfg.Database.VectorStore,
		VectorPath:        cfg.Database.VectorPath,
		APIKey:            cfg.Server.APIKey,
		RateLimitRPS:      cfg.Server.RateLimitRPS,
		SearchMode:        cfg.Search.Mode,
		ANNProfile:        cfg.Search.ANNProfile,
		ANNM:              cfg.Search.ANNM,
		ANNEfConstruct:    cfg.Search.ANNEfConstruct,
		ANNEfSearch:       cfg.Search.ANNEfSearch,
		ANNEvalSampleRate: cfg.Search.ANNEvalSampleRate,
		CacheEnabled:      cfg.Database.CacheEnabled,
		CacheMaxBytes:     cfg.Database.CacheMaxBytes,
		CacheMaxItems:     cfg.Database.CacheMaxItems,
		CacheTTL:          config.ParseDuration(cfg.Database.CacheTTL, 15*time.Minute),
		GRPCEnabled:       cfg.GRPC.Enabled,
		GRPCPort:          cfg.GRPC.Port,
		SecurityProfile:   cfg.Security.Profile,
		AuthEnabled:       cfg.Security.Auth.Enabled,
		AuthAPIKey:        cfg.Security.Auth.APIKey,
		GRPCAuthEnabled:   cfg.Security.Auth.GRPCEnabled,
		TLSEnabled:        cfg.Security.Transport.TLSEnabled,
		TLSCertFile:       cfg.Security.Transport.CertFile,
		TLSKeyFile:        cfg.Security.Transport.KeyFile,
		TrustForwardedFor: cfg.Security.Proxy.TrustForwardedFor,
		TrustedProxies:    cfg.Security.Proxy.TrustedProxies,
		StrictFilePerms:   cfg.Security.Storage.StrictFilePermissions,
		StorageDirMode:    cfg.Security.Storage.DirMode,
		StorageFileMode:   cfg.Security.Storage.FileMode,
	})
	return server, nil
}

type serverRunner interface {
	Start()
}

func runServer(server serverRunner) {
	server.Start()
}

func resolveConfigPath() string {
	defaultPath := "./configs/config.yaml"
	if envPath := strings.TrimSpace(os.Getenv("VECTOR_DB_CONFIG")); envPath != "" {
		defaultPath = envPath
	}

	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", defaultPath, "path to the configuration file")
	_ = fs.Parse(os.Args[1:])
	return *configPath
}

func mustExecute(executor func(string, func(serverRunner)) error, configPath string, runner func(serverRunner)) {
	if err := executor(configPath, runner); err != nil {
		logFatalf("failed to initialize server: %v", err)
	}
}

func serverAddr(cfg config.Config) string {
	port := cfg.Server.Port
	if port == "" {
		port = "19190"
	}
	if port[0] != ':' {
		return ":" + port
	}
	return port
}

func newHTTPServer(addr string, handler http.Handler, readTimeout, writeTimeout time.Duration) *http.Server {
	return &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}
}
