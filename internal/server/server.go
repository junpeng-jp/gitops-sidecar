package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/junpeng-jp/gitops-sidecar/internal/model"
	"github.com/junpeng-jp/gitops-sidecar/internal/storage"
)

type GitOpsServer struct {
	server     *http.Server
	controller *GitOpsController
	log        *slog.Logger
}

func NewServer(
	cfg *Config,
	state *storage.StateStore,
	workers map[string]*Worker,
	notifier *NotificationWorker,
	log *slog.Logger,
	version, commit, date string,
) *GitOpsServer {
	s := &GitOpsServer{
		controller: &GitOpsController{
			cfg:      cfg,
			state:    state,
			workers:  workers,
			notifier: notifier,
			log:      log,
			version:  version,
			commit:   commit,
			date:     date,
		},
		log: log,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /repos", s.handleGetRepos)
	mux.HandleFunc("GET /repos/{name}", s.handleGetRepo)
	mux.HandleFunc("POST /repos/{name}/operation", s.handleRepoOperation)
	mux.HandleFunc("POST /reset", s.handleReset)

	s.server = &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: defaultEnqueueTimeout,
	}

	return s
}

func (s *GitOpsServer) ListenAndServe() error {
	return s.server.ListenAndServe()
}

func (s *GitOpsServer) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func (s *GitOpsServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp, err := s.controller.Health(r.Context())
	s.writeResponse(w, http.StatusOK, resp, err)
}

const (
	defaultLimit = 10
	maxLimit     = 100
)

func (s *GitOpsServer) handleGetRepos(w http.ResponseWriter, r *http.Request) {
	limit := defaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err == nil {
			limit = n
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	req := model.GetReposRequest{Limit: limit}
	resp, err := s.controller.GetRepos(r.Context(), req)
	s.writeResponse(w, http.StatusOK, resp, err)
}

func (s *GitOpsServer) handleGetRepo(w http.ResponseWriter, r *http.Request) {
	req := model.GetRepoRequest{Name: r.PathValue("name")}
	resp, err := s.controller.GetRepo(r.Context(), req)
	s.writeResponse(w, http.StatusOK, resp, err)
}

func (s *GitOpsServer) handleRepoOperation(w http.ResponseWriter, r *http.Request) {
	var body model.RepoOperationBody
	err := json.NewDecoder(r.Body).Decode(&body)
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})

		return
	}
	req := model.RepoOperationRequest{
		Name: r.PathValue("name"),
		Body: body,
	}
	resp, err := s.controller.RepoOperation(r.Context(), req)
	s.writeResponse(w, http.StatusAccepted, resp, err)
}

func (s *GitOpsServer) handleReset(w http.ResponseWriter, r *http.Request) {
	resp, err := s.controller.Reset(r.Context())
	s.writeResponse(w, http.StatusAccepted, resp, err)
}

func (s *GitOpsServer) writeResponse(w http.ResponseWriter, successStatus int, v any, err error) {
	if err != nil {
		s.writeJSON(w, errorStatus(err), map[string]string{"error": err.Error()})

		return
	}
	s.writeJSON(w, successStatus, v)
}

func errorStatus(err error) int {
	switch {
	case errors.Is(err, errNotFound):
		return http.StatusNotFound
	case errors.Is(err, errConflict):
		return http.StatusConflict
	case errors.Is(err, errBadRequest):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func (s *GitOpsServer) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	err := json.NewEncoder(w).Encode(v)
	if err != nil {
		s.log.Error("writeJSON encode", "err", err)
	}
}
