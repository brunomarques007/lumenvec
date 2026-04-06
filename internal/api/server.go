package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"lumenvec/internal/core"
	"lumenvec/internal/index"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Server struct {
	router       *mux.Router
	port         string
	readTimeout  time.Duration
	writeTimeout time.Duration
	service      *core.Service
	maxBodyBytes int64
	apiKey       string
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

type ServerOptions struct {
	Port          string
	ReadTimeout   time.Duration
	WriteTimeout  time.Duration
	MaxBodyBytes  int64
	MaxVectorDim  int
	MaxK          int
	SnapshotPath  string
	WALPath       string
	SnapshotEvery int
	APIKey        string
	RateLimitRPS  int
	SearchMode    string
}

var defaultServerOptions = ServerOptions{
	Port:          ":19190",
	ReadTimeout:   10 * time.Second,
	WriteTimeout:  10 * time.Second,
	MaxBodyBytes:  1 << 20,
	MaxVectorDim:  4096,
	MaxK:          100,
	SnapshotPath:  "./data/snapshot.json",
	WALPath:       "./data/wal.log",
	SnapshotEvery: 25,
	APIKey:        "",
	RateLimitRPS:  100,
	SearchMode:    "exact",
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
		port:         opts.Port,
		readTimeout:  opts.ReadTimeout,
		writeTimeout: opts.WriteTimeout,
		service: core.NewService(core.ServiceOptions{
			MaxVectorDim:  opts.MaxVectorDim,
			MaxK:          opts.MaxK,
			SnapshotPath:  opts.SnapshotPath,
			WALPath:       opts.WALPath,
			SnapshotEvery: opts.SnapshotEvery,
			SearchMode:    opts.SearchMode,
		}),
		maxBodyBytes: opts.MaxBodyBytes,
		apiKey:       opts.APIKey,
		rateLimiter:  newRateLimiter(opts.RateLimitRPS, time.Second),
	}
	s.requestTotal, s.requestDuration, s.metricsRegistry = newMetricsRegistry()
	s.routes()
	return s
}

func applyDefaults(opts ServerOptions) ServerOptions {
	if strings.TrimSpace(opts.Port) == "" {
		opts.Port = defaultServerOptions.Port
	}
	if !strings.HasPrefix(opts.Port, ":") {
		opts.Port = ":" + opts.Port
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
	if opts.SnapshotEvery <= 0 {
		opts.SnapshotEvery = defaultServerOptions.SnapshotEvery
	}
	if opts.RateLimitRPS <= 0 {
		opts.RateLimitRPS = defaultServerOptions.RateLimitRPS
	}
	if strings.TrimSpace(opts.SearchMode) == "" {
		opts.SearchMode = defaultServerOptions.SearchMode
	}
	opts.SearchMode = strings.ToLower(strings.TrimSpace(opts.SearchMode))
	if opts.SearchMode != "ann" {
		opts.SearchMode = "exact"
	}
	return opts
}

func (s *Server) routes() {
	s.router.Use(s.accessLogMiddleware)
	s.router.Use(s.metricsMiddleware)
	s.router.Use(s.authMiddleware)
	s.router.Use(s.rateLimitMiddleware)

	s.router.HandleFunc("/health", s.HealthHandler).Methods(http.MethodGet)
	s.router.Handle("/metrics", promhttp.HandlerFor(s.metricsRegistry, promhttp.HandlerOpts{})).Methods(http.MethodGet)
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

func (s *Server) AddVectorHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
	var payload vectorPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid JSON payload", http.StatusBadRequest)
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
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
	var req batchVectorsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON payload", http.StatusBadRequest)
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

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(vectorPayload{ID: vec.ID, Values: vec.Values})
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
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
	var req searchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON payload", http.StatusBadRequest)
		return
	}
	results, err := s.service.Search(req.Values, req.K)
	if err != nil {
		http.Error(w, err.Error(), statusFromServiceError(err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

func (s *Server) SearchVectorsBatchHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
	var req batchSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON payload", http.StatusBadRequest)
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

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

func (s *Server) Start() {
	log.Printf("Starting server on port %s", s.port)
	server := &http.Server{
		Addr:         s.port,
		Handler:      s.router,
		ReadTimeout:  s.readTimeout,
		WriteTimeout: s.writeTimeout,
	}
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("Could not start server: %s", err)
	}
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
