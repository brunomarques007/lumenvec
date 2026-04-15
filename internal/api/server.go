package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/netip"
	"net/http"
	"os"
	"strings"
	"time"

	"lumenvec/internal/core"
	"lumenvec/internal/index"
	"lumenvec/internal/index/ann"

	"github.com/gorilla/mux"
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
	router       *mux.Router
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
	Vectors []vectorPayload `json:"vectors"`
}

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
	APIKey            string
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
	APIKey:            "",
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
		router:       mux.NewRouter(),
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
		authEnabled:  opts.AuthEnabled,
		grpcAuth:     opts.GRPCAuthEnabled,
		tlsEnabled:   opts.TLSEnabled,
		tlsCertFile:  opts.TLSCertFile,
		tlsKeyFile:   opts.TLSKeyFile,
		trustXFF:     opts.TrustForwardedFor,
		trustedCIDRs: parseTrustedProxies(opts.TrustedProxies),
		rateLimiter:  newRateLimiter(opts.RateLimitRPS, time.Second),
	}
	s.requestTotal, s.requestDuration, s.metricsRegistry = newMetricsRegistry(s.service)
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
	if opts.RateLimitRPS <= 0 {
		opts.RateLimitRPS = defaultServerOptions.RateLimitRPS
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
	s.router.Use(s.accessLogMiddleware)
	s.router.Use(s.metricsMiddleware)
	s.router.Use(s.authMiddleware)
	s.router.Use(s.rateLimitMiddleware)

	s.router.HandleFunc("/health", s.HealthHandler).Methods(http.MethodGet)
	s.router.Handle("/metrics", promhttp.HandlerFor(s.metricsRegistry, promhttp.HandlerOpts{})).Methods(http.MethodGet)
	s.router.HandleFunc("/vectors", s.ListVectorsHandler).Methods(http.MethodGet)
	s.router.HandleFunc("/vectors", s.AddVectorHandler).Methods(http.MethodPost)
	s.router.HandleFunc("/vectors/batch", s.AddVectorsBatchHandler).Methods(http.MethodPost)
	s.router.HandleFunc("/vectors/search", s.SearchVectorsHandler).Methods(http.MethodPost)
	s.router.HandleFunc("/vectors/search/batch", s.SearchVectorsBatchHandler).Methods(http.MethodPost)
	s.router.HandleFunc("/vectors/{id}", s.GetVectorHandler).Methods(http.MethodGet)
	s.router.HandleFunc("/vectors/{id}", s.DeleteVectorHandler).Methods(http.MethodDelete)
}

func (s *Server) Router() http.Handler {
	return s.router
}

func (s *Server) HealthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) ListVectorsHandler(w http.ResponseWriter, _ *http.Request) {
	vecs := s.service.ListVectors()
	out := make([]vectorPayload, 0, len(vecs))
	for _, vec := range vecs {
		out = append(out, vectorPayload{ID: vec.ID, Values: vec.Values})
	}
	writeJSON(w, 0, listVectorsResponse{Vectors: out})
}

func (s *Server) AddVectorHandler(w http.ResponseWriter, r *http.Request) {
	var payload vectorPayload
	if !s.readJSON(w, r, &payload) {
		return
	}
	if payload.ID == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	if err := s.service.AddVector(payload.ID, payload.Values); err != nil {
		if errors.Is(err, index.ErrVectorExists) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), statusFromServiceError(err))
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
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), statusFromServiceError(err))
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) GetVectorHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	vec, err := s.service.GetVector(id)
	if err != nil {
		if errors.Is(err, index.ErrVectorNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), statusFromServiceError(err))
		return
	}

	writeJSON(w, 0, vectorPayload{ID: vec.ID, Values: vec.Values})
}

func (s *Server) DeleteVectorHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if err := s.service.DeleteVector(id); err != nil {
		if errors.Is(err, index.ErrVectorNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), statusFromServiceError(err))
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
		http.Error(w, err.Error(), statusFromServiceError(err))
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
		http.Error(w, err.Error(), statusFromServiceError(err))
		return
	}
	writeJSON(w, 0, results)
}

func (s *Server) Start() {
	if s.grpcEnabled {
		logPrintfAPI("Starting gRPC server on port %s", s.grpcPort)
		listener, err := s.grpcListener()
		if err != nil {
			logFatalfAPI("Could not bind gRPC server: %s", err)
			return
		}
		if err := s.serveGRPC(listener); err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			logFatalfAPI("Could not start gRPC server: %s", err)
		}
		return
	}
	logPrintfAPI("Starting HTTP server on port %s", s.port)
	server := s.httpServer()
	var err error
	if s.tlsEnabled {
		err = listenAndServeTLSFunc(server, s.tlsCertFile, s.tlsKeyFile)
	} else {
		err = listenAndServeFunc(server)
	}
	if err != nil {
		logFatalfAPI("Could not start server: %s", err)
	}
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
		http.Error(w, "invalid JSON payload", http.StatusBadRequest)
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
