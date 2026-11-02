package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Mrprogramist/echo/internal/domain"
	"github.com/Mrprogramist/echo/internal/service"
)

type ChannelHandler struct {
	service *service.ChannelService
}

func NewChannelHandler(s *service.ChannelService) *ChannelHandler {
	return &ChannelHandler{service: s}
}

func (h *ChannelHandler) RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(authMiddleware)

		// 1. Каналы и категории конкретного сервера
		r.Route("/api/servers/{serverID}", func(r chi.Router) {
			r.Get("/layout", h.GetServerLayout)
			r.Post("/channels", h.CreateChannel)
			r.Get("/channels/deleted", h.GetDeletedChannels)
		})

		// 2. Действия над конкретным каналом
		r.Route("/api/channels/{channelID}", func(r chi.Router) {
			r.Get("/messages", h.GetMessages)
			r.Delete("/", h.DeleteChannel)
			r.Put("/mute", h.MuteChannel)
		})

		// 3. Отметка о прочтении
		r.Post("/api/read", h.MarkRead)
	})
}

func (h *ChannelHandler) GetServerLayout(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	serverID := chi.URLParam(r, "serverID")

	channels, categories, err := h.service.ListServerLayout(r.Context(), user, serverID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	resp := map[string]any{
		"channels":   channels,
		"categories": categories,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *ChannelHandler) CreateChannel(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	serverID := chi.URLParam(r, "serverID")

	var body struct {
		Name       string             `json:"name"`
		Kind       domain.ChannelKind `json:"kind"`
		CategoryID *string            `json:"categoryId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ch, err := h.service.CreateChannel(r.Context(), user, serverID, body.Name, body.Kind, body.CategoryID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(ch)
}

func (h *ChannelHandler) GetMessages(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	channelID := chi.URLParam(r, "channelID")

	beforeTime, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	beforeID := r.URL.Query().Get("beforeId")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	messages, err := h.service.GetChannelMessages(r.Context(), user, channelID, beforeTime, beforeID, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(messages)
}

func (h *ChannelHandler) DeleteChannel(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	channelID := chi.URLParam(r, "channelID")

	if err := h.service.DeleteChannel(r.Context(), user, channelID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *ChannelHandler) MuteChannel(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	channelID := chi.URLParam(r, "channelID")

	var body struct {
		Muted bool `json:"muted"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	mutedList, err := h.service.MuteChannel(r.Context(), user, channelID, body.Muted)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(mutedList)
}

func (h *ChannelHandler) GetDeletedChannels(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	serverID := chi.URLParam(r, "serverID")

	deleted, err := h.service.GetDeletedChannels(r.Context(), user, serverID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(deleted)
}

func (h *ChannelHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)

	var body struct {
		ConversationID string `json:"conversationId"`
		At             int64  `json:"at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.service.MarkRead(r.Context(), user.ID, body.ConversationID, body.At); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
