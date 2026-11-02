package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Mrprogramist/echo/internal/domain"
	"github.com/Mrprogramist/echo/internal/service"
)

type ServerHandler struct {
	service *service.ServerService
}

func NewServerHandler(s *service.ServerService) *ServerHandler {
	return &ServerHandler{service: s}
}

func (h *ServerHandler) RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler) {
	// Общедоступная отдача иконки сервера (с кэшированием)
	r.Get("/api/servers/{id}/icon", h.GetIcon)

	// Все остальные действия требуют авторизации
	r.Group(func(r chi.Router) {
		r.Use(authMiddleware)

		r.Post("/api/servers", h.CreateServer)
		r.Get("/api/servers", h.ListServers)

		r.Route("/api/servers/{id}", func(r chi.Router) {
			r.Get("/", h.GetServer)
			r.Patch("/", h.UpdateServer)
			r.Delete("/", h.DeleteServer)

			// Иконка
			r.Post("/icon", h.UploadIcon)
			r.Delete("/icon", h.DeleteIcon)

			// Участники
			r.Get("/members", h.ListMembers)
			r.Delete("/members/{userID}", h.KickMember)
			r.Post("/leave", h.LeaveServer)

			// Инвайты
			r.Get("/invites", h.ListInvites)
			r.Post("/invites", h.CreateInvite)
			r.Delete("/invites/{code}", h.DeleteInvite)
		})
	})
}

func (h *ServerHandler) CreateServer(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)

	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	srv, err := h.service.CreateServer(r.Context(), user, body.Name, body.Description)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(srv)
}

func (h *ServerHandler) ListServers(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)

	servers, err := h.service.ListUserServers(r.Context(), user)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(servers)
}

func (h *ServerHandler) GetServer(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	serverID := chi.URLParam(r, "id")

	srv, err := h.service.GetServerDetails(r.Context(), user, serverID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(srv)
}

func (h *ServerHandler) UpdateServer(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	serverID := chi.URLParam(r, "id")

	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	srv, err := h.service.UpdateServer(r.Context(), user, serverID, body.Name, body.Description)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(srv)
}

func (h *ServerHandler) DeleteServer(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	serverID := chi.URLParam(r, "id")

	if err := h.service.DeleteServer(r.Context(), user, serverID); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *ServerHandler) UploadIcon(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	serverID := chi.URLParam(r, "id")

	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	srv, err := h.service.UpdateServerIcon(r.Context(), user, serverID, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(srv)
}

func (h *ServerHandler) GetIcon(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	path, err := h.service.GetServerIconPath(r.Context(), serverID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if r.URL.Query().Get("v") != "" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, path)
}

func (h *ServerHandler) DeleteIcon(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	serverID := chi.URLParam(r, "id")

	srv, err := h.service.DeleteServerIcon(r.Context(), user, serverID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(srv)
}

func (h *ServerHandler) ListMembers(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	serverID := chi.URLParam(r, "id")

	members, err := h.service.ListMembers(r.Context(), user, serverID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(members)
}

func (h *ServerHandler) KickMember(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	serverID := chi.URLParam(r, "id")
	targetUserID := chi.URLParam(r, "userID")

	if err := h.service.KickMember(r.Context(), user, serverID, targetUserID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *ServerHandler) LeaveServer(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	serverID := chi.URLParam(r, "id")

	if err := h.service.LeaveServer(r.Context(), user, serverID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *ServerHandler) ListInvites(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	serverID := chi.URLParam(r, "id")

	invites, err := h.service.ListInvites(r.Context(), user, serverID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(invites)
}

func (h *ServerHandler) CreateInvite(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	serverID := chi.URLParam(r, "id")

	var body struct {
		AllowsRegistration bool `json:"allows_registration"`
		MaxUses            int  `json:"max_uses"`
		ExpiresAfterHours  int  `json:"expires_after_hours"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	inv, err := h.service.CreateInvite(r.Context(), user, serverID, body.AllowsRegistration, body.MaxUses, body.ExpiresAfterHours)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(inv)
}

func (h *ServerHandler) DeleteInvite(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	serverID := chi.URLParam(r, "id")
	code := chi.URLParam(r, "code")

	if err := h.service.DeleteInvite(r.Context(), user, serverID, code); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
