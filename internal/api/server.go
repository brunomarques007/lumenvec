package api

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lumenvec/internal/core"
	"lumenvec/internal/index"
	"lumenvec/internal/index/ann"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	listenAndServeFunc    = func(server *http.Server) error { return server.ListenAndServe() }
	listenAndServeTLSFunc = func(server *http.Server, certFile, keyFile string) error {
		return server.ListenAndServeTLS(certFile, keyFile)
	}
	logFatalfAPI = log.Fatalf
	logPrintfAPI = log.Printf
)

type Server struct {
	router       http.Handler
	protocol     string
	port         string
	grpcPort     string
	grpcEnabled  bool
	readTimeout  time.Duration
	writeTimeout time.Duration
	service      *core.Service
	maxBodyBytes int64
	apiKey       string
	authEnabled  bool
	grpcAuth     bool
	tlsEnabled   bool
	tlsCertFile  string
	tlsKeyFile   string
	accessLog    bool
	metrics      bool
	snapshotPath string
	walPath      string
	vectorStore  string
	vectorPath   string
	trustXFF     bool
	trustedCIDRs []netip.Prefix
	rateLimiter  *rateLimiter

	requestTotal    *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	metricsRegistry *prometheus.Registry
}

type vectorPayload struct {
	ID     string    `json:"id"`
	Values []float64 `json:"values"`
}

type searchRequest struct {
	Values []float64 `json:"values"`
	K      int       `json:"k"`
}

type batchVectorsRequest struct {
	Vectors []vectorPayload `json:"vectors"`
}

type batchSearchQuery struct {
	ID     string    `json:"id"`
	Values []float64 `json:"values"`
	K      int       `json:"k"`
}

type batchSearchRequest struct {
	Queries []batchSearchQuery `json:"queries"`
}

type listVectorsResponse struct {
	Vectors    []vectorPayload `json:"vectors"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

const (
	defaultListVectorsLimit = 100
	maxListVectorsLimit     = 1000
)

type ServerOptions struct {
	Protocol          string
	Port              string
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	MaxBodyBytes      int64
	MaxVectorDim      int
	MaxK              int
	SnapshotPath      string
	WALPath           string
	SnapshotEvery     int
	VectorStore       string
	VectorPath        string
	SyncEvery         int
	APIKey            string
	MetricsEnabled    bool
	DisableRateLimit  bool
	RateLimitRPS      int
	SearchMode        string
	ANNProfile        string
	ANNM              int
	ANNEfConstruct    int
	ANNEfSearch       int
	ANNEvalSampleRate int
	CacheEnabled      bool
	CacheMaxBytes     int64
	CacheMaxItems     int
	CacheTTL          time.Duration
	GRPCEnabled       bool
	GRPCPort          string
	SecurityProfile   string
	AuthEnabled       bool
	AuthAPIKey        string
	GRPCAuthEnabled   bool
	TLSEnabled        bool
	TLSCertFile       string
	TLSKeyFile        string
	AccessLogEnabled  bool
	TrustForwardedFor bool
	TrustedProxies    []string
	StrictFilePerms   bool
	StorageDirMode    string
	StorageFileMode   string
}

var defaultServerOptions = ServerOptions{
	Protocol:          "http",
	Port:              ":19190",
	ReadTimeout:       10 * time.Second,
	WriteTimeout:      10 * time.Second,
	MaxBodyBytes:      1 << 20,
	MaxVectorDim:      4096,
	MaxK:              100,
	SnapshotPath:      "./data/snapshot.json",
	WALPath:           "./data/wal.log",
	SnapshotEvery:     25,
	VectorStore:       "memory",
	VectorPath:        "./data/vectors",
	SyncEvery:         1,
	APIKey:            "",
	MetricsEnabled:    true,
	RateLimitRPS:      100,
	SearchMode:        "exact",
	ANNProfile:        "balanced",
	ANNM:              16,
	ANNEfConstruct:    64,
	ANNEfSearch:       64,
	ANNEvalSampleRate: 0,
	CacheEnabled:      false,
	CacheMaxBytes:     8 << 20,
	CacheMaxItems:     1024,
	CacheTTL:          15 * time.Minute,
	GRPCEnabled:       false,
	GRPCPort:          ":19191",
	AccessLogEnabled:  false,
}

func NewServer(port string) *Server {
	opts := defaultServerOptions
	if strings.TrimSpace(port) != "" {
		opts.Port = port
	}
	return NewServerWithOptions(opts)
}

func NewServerWithOptions(opts ServerOptions) *Server {
	opts = applyDefaults(opts)

	s := &Server{
		protocol:     opts.Protocol,
		port:         opts.Port,
		grpcPort:     opts.GRPCPort,
		grpcEnabled:  opts.GRPCEnabled,
		readTimeout:  opts.ReadTimeout,
		writeTimeout: opts.WriteTimeout,
		service: core.NewService(core.ServiceOptions{
			MaxVectorDim:  opts.MaxVectorDim,
			MaxK:          opts.MaxK,
			SnapshotPath:  opts.SnapshotPath,
			WALPath:       opts.WALPath,
			SnapshotEvery: opts.SnapshotEvery,
			SearchMode:    opts.SearchMode,
			ANNProfile:    opts.ANNProfile,
			ANNOptions: ann.Options{
				M:              opts.ANNM,
				EfConstruction: opts.ANNEfConstruct,
				EfSearch:       opts.ANNEfSearch,
			},
			ANNEvalSampleRate: opts.ANNEvalSampleRate,
			VectorStore:       opts.VectorStore,
			VectorPath:        opts.VectorPath,
			SyncEvery:         opts.SyncEvery,
			StorageSecurity: core.StorageSecurityOptions{
				StrictFilePermissions: opts.StrictFilePerms,
				DirMode:               core.ParseFileMode(opts.StorageDirMode, os.FileMode(0o755)),
				FileMode:              core.ParseFileMode(opts.StorageFileMode, os.FileMode(0o644)),
			},
			Cache: core.CacheOptions{
				Enabled:  opts.CacheEnabled,
				MaxBytes: opts.CacheMaxBytes,
				MaxItems: opts.CacheMaxItems,
				TTL:      opts.CacheTTL,
			},
		}),
		maxBodyBytes: opts.MaxBodyBytes,
		apiKey:       firstNonEmpty(opts.AuthAPIKey, opts.APIKey),
		metrics:      opts.MetricsEnabled,
		authEnabled:  opts.AuthEnabled,
		grpcAuth:     opts.GRPCAuthEnabled,
		tlsEnabled:   opts.TLSEnabled,
		tlsCertFile:  opts.TLSCertFile,
		tlsKeyFile:   opts.TLSKeyFile,
		accessLog:    opts.AccessLogEnabled,
		trustXFF:     opts.TrustForwardedFor,
		snapshotPath: opts.SnapshotPath,
		walPath:      opts.WALPath,
		vectorStore:  opts.VectorStore,
		vectorPath:   opts.VectorPath,
		trustedCIDRs: parseTrustedProxies(opts.TrustedProxies),
		rateLimiter:  newRateLimiter(opts.RateLimitRPS, time.Second),
	}
	if s.metrics {
		s.requestTotal, s.requestDuration, s.metricsRegistry = newMetricsRegistry(s.service)
	}
	s.routes()
	return s
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func parseTrustedProxies(values []string) []netip.Prefix {
	parsed := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(value); err == nil {
			parsed = append(parsed, prefix)
			continue
		}
		if addr, err := netip.ParseAddr(value); err == nil {
			parsed = append(parsed, netip.PrefixFrom(addr, addr.BitLen()))
		}
	}
	return parsed
}

func (s *Server) isTrustedProxy(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return false
	}
	for _, prefix := range s.trustedCIDRs {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func validateAPIKey(provided, expected string) bool {
	provided = strings.TrimSpace(provided)
	expected = strings.TrimSpace(expected)
	if provided == "" || expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func authKeyFromHTTPRequest(r *http.Request) string {
	key := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if key != "" {
		return key
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

func applyDefaults(opts ServerOptions) ServerOptions {
	opts.Protocol = normalizeServerProtocol(opts.Protocol)
	if strings.TrimSpace(opts.Port) == "" {
		opts.Port = defaultServerOptions.Port
	}
	if !strings.HasPrefix(opts.Port, ":") {
		opts.Port = ":" + opts.Port
	}
	if strings.TrimSpace(opts.GRPCPort) == "" {
		opts.GRPCPort = defaultServerOptions.GRPCPort
	}
	if !strings.HasPrefix(opts.GRPCPort, ":") {
		opts.GRPCPort = ":" + opts.GRPCPort
	}
	if opts.ReadTimeout <= 0 {
		opts.ReadTimeout = defaultServerOptions.ReadTimeout
	}
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = defaultServerOptions.WriteTimeout
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = defaultServerOptions.MaxBodyBytes
	}
	if opts.MaxVectorDim <= 0 {
		opts.MaxVectorDim = defaultServerOptions.MaxVectorDim
	}
	if opts.MaxK <= 0 {
		opts.MaxK = defaultServerOptions.MaxK
	}
	if strings.TrimSpace(opts.SnapshotPath) == "" {
		opts.SnapshotPath = defaultServerOptions.SnapshotPath
	}
	if strings.TrimSpace(opts.WALPath) == "" {
		opts.WALPath = defaultServerOptions.WALPath
	}
	if strings.TrimSpace(opts.VectorStore) == "" {
		opts.VectorStore = defaultServerOptions.VectorStore
	}
	if strings.TrimSpace(opts.VectorPath) == "" {
		opts.VectorPath = defaultServerOptions.VectorPath
	}
	if opts.SnapshotEvery <= 0 {
		opts.SnapshotEvery = defaultServerOptions.SnapshotEvery
	}
	if opts.SyncEvery <= 0 {
		opts.SyncEvery = defaultServerOptions.SyncEvery
	}
	if !opts.DisableRateLimit && opts.RateLimitRPS <= 0 {
		opts.RateLimitRPS = defaultServerOptions.RateLimitRPS
	}
	if opts.DisableRateLimit {
		opts.RateLimitRPS = 0
	}
	if strings.TrimSpace(opts.SearchMode) == "" {
		opts.SearchMode = defaultServerOptions.SearchMode
	}
	if strings.TrimSpace(opts.ANNProfile) == "" {
		opts.ANNProfile = defaultServerOptions.ANNProfile
	}
	if opts.ANNM <= 0 {
		opts.ANNM = defaultServerOptions.ANNM
	}
	if opts.ANNEfConstruct <= 0 {
		opts.ANNEfConstruct = defaultServerOptions.ANNEfConstruct
	}
	if opts.ANNEfSearch <= 0 {
		opts.ANNEfSearch = defaultServerOptions.ANNEfSearch
	}
	if opts.ANNEvalSampleRate < 0 {
		opts.ANNEvalSampleRate = defaultServerOptions.ANNEvalSampleRate
	}
	if opts.CacheMaxItems <= 0 {
		opts.CacheMaxItems = defaultServerOptions.CacheMaxItems
	}
	if opts.CacheMaxBytes <= 0 {
		opts.CacheMaxBytes = defaultServerOptions.CacheMaxBytes
	}
	if opts.CacheTTL <= 0 {
		opts.CacheTTL = defaultServerOptions.CacheTTL
	}
	opts.GRPCEnabled = opts.Protocol == "grpc"
	opts.SearchMode = strings.ToLower(strings.TrimSpace(opts.SearchMode))
	if opts.SearchMode != "ann" {
		opts.SearchMode = "exact"
	}
	return opts
}

func normalizeServerProtocol(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "grpc":
		return "grpc"
	default:
		return "http"
	}
}

func (s *Server) routes() {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", methodHandler(http.MethodGet, s.HealthHandler))
	mux.HandleFunc("/livez", methodHandler(http.MethodGet, s.LivenessHandler))
	mux.HandleFunc("/readyz", methodHandler(http.MethodGet, s.ReadinessHandler))
	mux.HandleFunc("/v1/health", methodHandler(http.MethodGet, s.HealthHandler))
	mux.HandleFunc("/v1/livez", methodHandler(http.MethodGet, s.LivenessHandler))
	mux.HandleFunc("/v1/readyz", methodHandler(http.MethodGet, s.ReadinessHandler))
	if s.metrics && s.metricsRegistry != nil {
		mux.Handle("/metrics", methodHandler(http.MethodGet, promhttp.HandlerFor(s.metricsRegistry, promhttp.HandlerOpts{}).ServeHTTP))
	}
	s.registerVectorRoutes(mux, "")
	s.registerVectorRoutes(mux, "/v1")

	var handler http.Handler = mux
	handler = s.accessLogMiddleware(handler)
	if s.metrics {
		handler = s.metricsMiddleware(handler)
	}
	if s.authEnabled && s.apiKey != "" {
		handler = s.authMiddleware(handler)
	}
	if s.rateLimiter != nil {
		handler = s.rateLimitMiddleware(handler)
	}
	handler = s.requestIDMiddleware(handler)
	s.router = handler
}

func (s *Server) registerVectorRoutes(mux *http.ServeMux, prefix string) {
	mux.HandleFunc(prefix+"/vectors", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.ListVectorsHandler(w, r)
		case http.MethodPost:
			s.AddVectorHandler(w, r)
		default:
			methodNotAllowed(w, r)
		}
	})
	mux.HandleFunc(prefix+"/vectors/batch", methodHandler(http.MethodPost, s.AddVectorsBatchHandler))
	mux.HandleFunc(prefix+"/vectors/search", methodHandler(http.MethodPost, s.SearchVectorsHandler))
	mux.HandleFunc(prefix+"/vectors/search/batch", methodHandler(http.MethodPost, s.SearchVectorsBatchHandler))
	mux.HandleFunc(prefix+"/vectors/", s.vectorByIDHandler)
}

func (s *Server) Router() http.Handler {
	return s.router
}

func methodHandler(method string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			methodNotAllowed(w, r)
			return
		}
		handler(w, r)
	}
}

func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}

func (s *Server) vectorByIDHandler(w http.ResponseWriter, r *http.Request) {
	if vectorIDFromPath(r.URL.Path) == "" {
		writeError(w, r, http.StatusNotFound, "not_found", "vector not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.GetVectorHandler(w, r)
	case http.MethodDelete:
		s.DeleteVectorHandler(w, r)
	default:
		methodNotAllowed(w, r)
	}
}

func (s *Server) HealthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) LivenessHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) ReadinessHandler(w http.ResponseWriter, r *http.Request) {
	if err := s.readinessCheck(); err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "not_ready", err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}

func (s *Server) readinessCheck() error {
	if s == nil || s.service == nil {
		return errors.New("service is not initialized")
	}
	if strings.EqualFold(strings.TrimSpace(s.vectorStore), "disk") {
		if err := ensureWritableDir(s.vectorPath); err != nil {
			return fmt.Errorf("vector path is not writable: %w", err)
		}
		return nil
	}
	if err := ensureWritableParent(s.snapshotPath); err != nil {
		return fmt.Errorf("snapshot path is not writable: %w", err)
	}
	if err := ensureWritableParent(s.walPath); err != nil {
		return fmt.Errorf("wal path is not writable: %w", err)
	}
	return nil
}

func ensureWritableParent(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("path is empty")
	}
	return ensureWritableDir(filepath.Dir(path))
}

func ensureWritableDir(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("directory is empty")
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return nil
}

func (s *Server) ListVectorsHandler(w http.ResponseWriter, r *http.Request) {
	// Optional query params:
	// - limit (int): maximum number of vectors to return, capped by maxListVectorsLimit
	// - cursor (string): opaque base64-encoded last ID (exclusive)
	// - after (string): legacy raw id cursor (backwards compat)
	// - ids_only (bool): when true, return only ids (no values)
	q := r.URL.Query()
	limit, ok := parseListVectorsLimit(q)
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_argument", "limit must be a positive integer")
		return
	}

	afterID := strings.TrimSpace(q.Get("after"))
	if cursorVal := strings.TrimSpace(q.Get("cursor")); cursorVal != "" {
		decoded, err := decodeListVectorsCursor(cursorVal)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_argument", "cursor must be a valid list cursor")
			return
		}
		afterID = decoded
	}

	idsOnly := false
	if v := strings.TrimSpace(q.Get("ids_only")); v != "" {
		if v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes") {
			idsOnly = true
		}
	}

	page := s.service.ListVectorsPage(core.ListVectorsOptions{
		AfterID: afterID,
		Limit:   limit,
		IDsOnly: idsOnly,
	})
	var nextCursor string
	if page.NextCursor != "" {
		nextCursor = encodeListVectorsCursor(page.NextCursor)
	}

	if idsOnly {
		payload := struct {
			Vectors []struct {
				ID string `json:"id"`
			} `json:"vectors"`
			NextCursor string `json:"next_cursor,omitempty"`
		}{Vectors: make([]struct {
			ID string `json:"id"`
		}, 0, len(page.Vectors)), NextCursor: nextCursor}
		for _, v := range page.Vectors {
			payload.Vectors = append(payload.Vectors, struct {
				ID string `json:"id"`
			}{ID: v.ID})
		}
		writeJSON(w, 0, payload)
		return
	}

	out := make([]vectorPayload, 0, len(page.Vectors))
	for _, vec := range page.Vectors {
		out = append(out, vectorPayload{ID: vec.ID, Values: vec.Values})
	}
	writeJSON(w, 0, listVectorsResponse{Vectors: out, NextCursor: nextCursor})
}

func parseListVectorsLimit(values url.Values) (int, bool) {
	raw := strings.TrimSpace(values.Get("limit"))
	if raw == "" {
		return defaultListVectorsLimit, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return 0, false
	}
	if limit > maxListVectorsLimit {
		return maxListVectorsLimit, true
	}
	return limit, true
}

func decodeListVectorsCursor(cursor string) (string, error) {
	if decoded, err := base64.RawURLEncoding.DecodeString(cursor); err == nil {
		return string(decoded), nil
	}
	decoded, err := base64.URLEncoding.DecodeString(cursor)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func encodeListVectorsCursor(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}

func (s *Server) AddVectorHandler(w http.ResponseWriter, r *http.Request) {
	var payload vectorPayload
	if !s.readJSON(w, r, &payload) {
		return
	}
	if payload.ID == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_argument", "id is required")
		return
	}
	if err := s.service.AddVector(payload.ID, payload.Values); err != nil {
		if errors.Is(err, index.ErrVectorExists) {
			writeError(w, r, http.StatusConflict, "already_exists", err.Error())
			return
		}
		writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) AddVectorsBatchHandler(w http.ResponseWriter, r *http.Request) {
	var req batchVectorsRequest
	if !s.readJSON(w, r, &req) {
		return
	}
	vectors := make([]index.Vector, 0, len(req.Vectors))
	for _, vec := range req.Vectors {
		vectors = append(vectors, index.Vector{ID: vec.ID, Values: vec.Values})
	}
	if err := s.service.AddVectors(vectors); err != nil {
		if errors.Is(err, index.ErrVectorExists) {
			writeError(w, r, http.StatusConflict, "already_exists", err.Error())
			return
		}
		writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) GetVectorHandler(w http.ResponseWriter, r *http.Request) {
	id := vectorIDFromPath(r.URL.Path)
	vec, err := s.service.GetVector(id)
	if err != nil {
		if errors.Is(err, index.ErrVectorNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", err.Error())
			return
		}
		writeServiceError(w, r, err)
		return
	}

	writeJSON(w, 0, vectorPayload{ID: vec.ID, Values: vec.Values})
}

func (s *Server) DeleteVectorHandler(w http.ResponseWriter, r *http.Request) {
	id := vectorIDFromPath(r.URL.Path)
	if err := s.service.DeleteVector(id); err != nil {
		if errors.Is(err, index.ErrVectorNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", err.Error())
			return
		}
		writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) SearchVectorsHandler(w http.ResponseWriter, r *http.Request) {
	var req searchRequest
	if !s.readJSON(w, r, &req) {
		return
	}
	results, err := s.service.Search(req.Values, req.K)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, 0, results)
}

func (s *Server) SearchVectorsBatchHandler(w http.ResponseWriter, r *http.Request) {
	var req batchSearchRequest
	if !s.readJSON(w, r, &req) {
		return
	}
	queries := make([]core.BatchSearchQuery, 0, len(req.Queries))
	for _, query := range req.Queries {
		queries = append(queries, core.BatchSearchQuery{ID: query.ID, Values: query.Values, K: query.K})
	}
	results, err := s.service.SearchBatch(queries)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, 0, results)
}

func (s *Server) Start() {
	if err := s.Run(context.Background()); err != nil {
		logFatalfAPI("Could not start server: %s", err)
	}
}

func (s *Server) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() { _ = s.Close() }()

	if s.grpcEnabled {
		logPrintfAPI("Starting gRPC server on port %s", s.grpcPort)
		listener, err := s.grpcListener()
		if err != nil {
			return fmt.Errorf("bind gRPC server: %w", err)
		}
		return s.runGRPC(ctx, listener)
	}

	logPrintfAPI("Starting HTTP server on port %s", s.port)
	return s.runHTTP(ctx)
}

func (s *Server) runHTTP(ctx context.Context) error {
	server := s.httpServer()
	errCh := make(chan error, 1)
	go func() {
		var err error
		if s.tlsEnabled {
			err = listenAndServeTLSFunc(server, s.tlsCertFile, s.tlsKeyFile)
		} else {
			err = listenAndServeFunc(server)
		}
		errCh <- err
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	}
}

func (s *Server) runGRPC(ctx context.Context, listener net.Listener) error {
	server, err := s.grpcServer()
	if err != nil {
		_ = listener.Close()
		return err
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- grpcServeFunc(server, listener)
	}()

	select {
	case <-ctx.Done():
		stopped := make(chan struct{})
		go func() {
			server.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			server.Stop()
		}
		if err := <-errCh; err != nil && !errors.Is(err, net.ErrClosed) {
			return fmt.Errorf("serve gRPC: %w", err)
		}
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return fmt.Errorf("serve gRPC: %w", err)
		}
		return nil
	}
}

func (s *Server) Close() error {
	if s == nil || s.service == nil {
		return nil
	}
	return s.service.Close()
}

func (s *Server) httpServer() *http.Server {
	return &http.Server{
		Addr:         s.port,
		Handler:      s.router,
		ReadTimeout:  s.readTimeout,
		WriteTimeout: s.writeTimeout,
	}
}

const contentTypeJSON = "application/json"

func (s *Server) readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_json", "invalid JSON payload")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	if status != 0 {
		w.WriteHeader(status)
	}
	_ = json.NewEncoder(w).Encode(v)
}

type errorResponse struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	writeError(w, r, statusFromServiceError(err), errorCodeFromStatus(statusFromServiceError(err)), err.Error())
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	if !isV1Request(r) {
		http.Error(w, message, status)
		return
	}
	if strings.TrimSpace(code) == "" {
		code = errorCodeFromStatus(status)
	}
	writeJSON(w, status, errorResponse{Error: apiError{Code: code, Message: message}})
}

func isV1Request(r *http.Request) bool {
	return r != nil && (r.URL.Path == "/v1" || strings.HasPrefix(r.URL.Path, "/v1/"))
}

func vectorIDFromPath(path string) string {
	path = strings.TrimPrefix(path, "/v1")
	return strings.TrimPrefix(path, "/vectors/")
}

func statusFromServiceError(err error) int {
	switch {
	case errors.Is(err, core.ErrInvalidID),
		errors.Is(err, core.ErrInvalidValues),
		errors.Is(err, core.ErrInvalidK),
		errors.Is(err, core.ErrVectorDimTooHigh),
		errors.Is(err, core.ErrKTooHigh):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func errorCodeFromStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_argument"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "already_exists"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusTooManyRequests:
		return "rate_limited"
	default:
		return "internal"
	}
}
