package main

import (
	"fmt"
	"log"
	"lumenvec/internal/api"
	"lumenvec/internal/config"
	"net/http"
	"time"
)

var (
	executeFunc = execute
	logFatalf   = log.Fatalf
	logInfof    = log.Println
)

func main() {
	mustExecute(executeFunc, "./configs/config.yaml", runServer)
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
		Port:          cfg.Server.Port,
		ReadTimeout:   config.ParseDuration(cfg.Server.ReadTimeout, 10*time.Second),
		WriteTimeout:  config.ParseDuration(cfg.Server.WriteTimeout, 10*time.Second),
		MaxBodyBytes:  cfg.Limits.MaxBodyBytes,
		MaxVectorDim:  cfg.Limits.MaxVectorDim,
		MaxK:          cfg.Limits.MaxK,
		SnapshotPath:  cfg.Database.SnapshotPath,
		WALPath:       cfg.Database.WALPath,
		SnapshotEvery: cfg.Database.SnapshotEvery,
		APIKey:        cfg.Server.APIKey,
		RateLimitRPS:  cfg.Server.RateLimitRPS,
		SearchMode:    cfg.Search.Mode,
	})
	return server, nil
}

type serverRunner interface {
	Start()
}

func runServer(server serverRunner) {
	server.Start()
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
