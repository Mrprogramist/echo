package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Mrprogramist/echo/internal/domain"
	"github.com/Mrprogramist/echo/internal/service"
)

type DMHandler struct {
	service *service.DMService
}

func NewDMHandler(s *service.DMService) *DMHandler {
	return &DMHandler{service: s}
}

func (h *DMHandler) RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(authMiddleware)

		// 1. Личные переписки (1-на-1)
		r.Get("/api/dms", h.ListDMs)
		r.Post("/api/dms", h.OpenDM)
		r.Get("/api/dms/{dmID}/messages", h.GetMessages)

		// 2. Группы
		r.Post("/api/groups", h.CreateGroup)
		r.Post("/api/groups/{groupID}/members", h.AddMembers)
		r.Patch("/api/groups/{groupID}", h.RenameGroup)
		r.Delete("/api/groups/{groupID}/members/me", h.LeaveGroup)
	})
}

func (h *DMHandler) ListDMs(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)

	dms, err := h.service.ListDMs(r.Context(), user)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dms)
}

func (h *DMHandler) OpenDM(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)

	var body struct {
		UserID string `json:"userId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	dm, err := h.service.OpenDM(r.Context(), user, body.UserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(dm)
}

func (h *DMHandler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)

	var body struct {
		Name    string   `json:"name"`
		UserIDs []string `json:"userIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	group, err := h.service.CreateGroup(r.Context(), user, body.Name, body.UserIDs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(group)
}

func (h *DMHandler) AddMembers(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	groupID := chi.URLParam(r, "groupID")

	var body struct {
		UserIDs []string `json:"userIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	members, err := h.service.AddGroupMembers(r.Context(), user, groupID, body.UserIDs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"members": members})
}

func (h *DMHandler) RenameGroup(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	groupID := chi.URLParam(r, "groupID")

	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	newName, err := h.service.RenameGroup(r.Context(), user, groupID, body.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"name": newName})
}

func (h *DMHandler) LeaveGroup(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	groupID := chi.URLParam(r, "groupID")

	if err := h.service.LeaveGroup(r.Context(), user, groupID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *DMHandler) GetMessages(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	dmID := chi.URLParam(r, "dmID")

	beforeTime, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	beforeID := r.URL.Query().Get("beforeId")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	messages, err := h.service.GetDMMessages(r.Context(), user, dmID, beforeTime, beforeID, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(messages)
}
