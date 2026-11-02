package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Mrprogramist/echo/internal/domain"
	"github.com/Mrprogramist/echo/internal/service"
)

type InstanceHandler struct {
	service *service.InstanceService
}

func NewInstanceHandler(s *service.InstanceService) *InstanceHandler {
	return &InstanceHandler{service: s}
}

// RegisterRoutes регистрирует маршруты для платформы и алиасы для старого клиента
func (h *InstanceHandler) RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler) {
	// 1. Манифест PWA
	r.Get("/manifest.webmanifest", h.GetManifest)

	// 2. Новые маршруты платформы (/api/instance)
	r.Route("/api/instance", func(r chi.Router) {
		r.Get("/", h.GetInfo)
		r.Get("/icon", h.GetIcon)

		// Защищенные роуты (только для авторизованных суперадминов)
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)
			r.Patch("/", h.UpdateName)
			r.Post("/icon", h.UploadIcon)
			r.Delete("/icon", h.DeleteIcon)
		})
	})

	// 3. Алиасы для обратной совместимости со старым веб-клиентом (/api/server)
	r.Route("/api/server", func(r chi.Router) {
		r.Get("/", h.GetInfo)
		r.Get("/icon", h.GetIcon)

		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)
			r.Patch("/", h.UpdateName)
			r.Post("/icon", h.UploadIcon)
			r.Delete("/icon", h.DeleteIcon)
		})
	})
}

func (h *InstanceHandler) GetInfo(w http.ResponseWriter, r *http.Request) {
	info, err := h.service.GetInfo(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

func (h *InstanceHandler) GetIcon(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query().Get("v")
	path, currentVersion, err := h.service.GetIconPath(r.Context())
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Кэширование: если клиент запрашивает конкретную версию — кэшируем на год
	if v != "" && v == currentVersion {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}

	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "image/png")
	http.ServeFile(w, r, path)
}

func (h *InstanceHandler) UpdateName(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)

	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	info, err := h.service.UpdateName(r.Context(), user, body.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

func (h *InstanceHandler) UploadIcon(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)

	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	info, err := h.service.UpdateIcon(r.Context(), user, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

func (h *InstanceHandler) DeleteIcon(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)

	info, err := h.service.DeleteIcon(r.Context(), user)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

func (h *InstanceHandler) GetManifest(w http.ResponseWriter, r *http.Request) {
	manifestBytes, err := h.service.GenerateManifest(r.Context())
	if err != nil {
		http.Error(w, "failed to build manifest", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(manifestBytes)
}
