package http

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Mrprogramist/echo/internal/apierror"
	"github.com/Mrprogramist/echo/internal/domain"
	"github.com/Mrprogramist/echo/internal/service"
)

type DiscordImportHandler struct {
	importService *service.DiscordImportService
}

func NewDiscordImportHandler(importService *service.DiscordImportService) *DiscordImportHandler {
	return &DiscordImportHandler{importService: importService}
}

func (h *DiscordImportHandler) RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(authMiddleware)

		// Предпросмотр шаблона (можно смотреть перед импортом в любой сервер)
		r.Post("/api/import/discord/preview", h.Preview)

		// Импорт в конкретный сервер
		r.Post("/api/servers/{serverID}/import/discord", h.Import)
	})
}

func (h *DiscordImportHandler) Preview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Link string `json:"link"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		apierror.WriteJSON(w, apierror.New(http.StatusBadRequest, "badRequest", "Неверный запрос"))
		return
	}

	structure, err := h.importService.FetchStructure(r.Context(), body.Link)
	if err != nil {
		apierror.WriteJSON(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(structure)
}

func (h *DiscordImportHandler) Import(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	serverID := chi.URLParam(r, "serverID")

	var body struct {
		Link string   `json:"link"`
		Skip []string `json:"skip"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		apierror.WriteJSON(w, apierror.New(http.StatusBadRequest, "badRequest", "Неверный запрос"))
		return
	}

	err := h.importService.ImportToServer(r.Context(), user, serverID, body.Link, body.Skip)
	if err != nil {
		apierror.WriteJSON(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
