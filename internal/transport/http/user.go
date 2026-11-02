package http

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"github.com/Mrprogramist/echo/internal/apierror"
	"github.com/Mrprogramist/echo/internal/database"
	"github.com/Mrprogramist/echo/internal/domain"
	"github.com/Mrprogramist/echo/internal/service"
)

type UserHandler struct {
	userService   *service.UserService
	serverService *service.ServerService
	avatarDir     string
}

func NewUserHandler(userService *service.UserService, serverService *service.ServerService, avatarDir string) *UserHandler {
	return &UserHandler{
		userService:   userService,
		serverService: serverService,
		avatarDir:     avatarDir,
	}
}

func (h *UserHandler) RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler) {
	// Публичные маршруты
	r.Post("/api/setup", h.Setup)
	r.Post("/api/register", h.Register)
	r.Post("/api/login", h.Login)
	r.Post("/api/recover", h.Recover)

	// Сброс пароля по ссылке от админа
	r.Post("/api/reset/check", h.CheckReset)
	r.Post("/api/reset", h.ResetPassword)

	// Отдача аватарок (статические файлы)
	r.Get("/api/avatars/{name}", h.GetAvatar)

	// Защищенные маршруты
	r.Group(func(r chi.Router) {
		r.Use(authMiddleware)

		r.Get("/api/bootstrap", h.Bootstrap)
		r.Post("/api/logout-all", h.LogoutAll)

		// Профиль и статус
		r.Patch("/api/me", h.UpdateBio)
		r.Patch("/api/me/name", h.ChangeName)
		r.Post("/api/me/password", h.ChangePassword)
		r.Patch("/api/me/status", h.SetStatus)

		// Аватарка
		r.Post("/api/me/avatar", h.UploadAvatar)
		r.Delete("/api/me/avatar", h.DeleteAvatar)

		// Блокировки пользователей
		r.Put("/api/blocks/{otherID}", h.BlockUser)
		r.Delete("/api/blocks/{otherID}", h.UnblockUser)

		// Администрирование пользователей
		r.Post("/api/users/{userID}/reset-link", h.MakeResetLink)
		r.Post("/api/users/{userID}/remove", h.RemoveUser)
		r.Delete("/api/users/{userID}/removed", h.LetBackUser)
	})
}

func (h *UserHandler) Setup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code       string `json:"code"`
		ServerName string `json:"serverName"`
		Name       string `json:"name"`
		Password   string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		apierror.WriteJSON(w, apierror.New(http.StatusBadRequest, "badRequest", "Неверный запрос"))
		return
	}

	payload, err := h.userService.Setup(r.Context(), body.Code, body.ServerName, body.Name, body.Password)
	if err != nil {
		apierror.WriteJSON(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *UserHandler) Register(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Invite   string `json:"invite"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		apierror.WriteJSON(w, apierror.New(http.StatusBadRequest, "badRequest", "Неверный запрос"))
		return
	}

	payload, err := h.userService.Register(r.Context(), body.Invite, body.Name, body.Password)
	if err != nil {
		apierror.WriteJSON(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *UserHandler) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		apierror.WriteJSON(w, apierror.New(http.StatusBadRequest, "badRequest", "Неверный запрос"))
		return
	}

	payload, err := h.userService.Login(r.Context(), body.Name, body.Password)
	if err != nil {
		apierror.WriteJSON(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *UserHandler) Recover(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Key      string `json:"key"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		apierror.WriteJSON(w, apierror.New(http.StatusBadRequest, "badRequest", "Неверный запрос"))
		return
	}

	payload, err := h.userService.RecoverPassword(r.Context(), body.Name, body.Key, body.Password)
	if err != nil {
		apierror.WriteJSON(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *UserHandler) MakeResetLink(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	targetUserID := chi.URLParam(r, "userID")

	token, expiresAt, err := h.userService.CreateResetLink(r.Context(), user, targetUserID)
	if err != nil {
		apierror.WriteJSON(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"token":     token,
		"expiresAt": expiresAt,
	})
}

func (h *UserHandler) CheckReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		apierror.WriteJSON(w, apierror.New(http.StatusBadRequest, "badRequest", "Неверный запрос"))
		return
	}

	name, err := h.userService.CheckResetLink(r.Context(), body.Token)
	if err != nil {
		apierror.WriteJSON(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"name": name})
}

func (h *UserHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		apierror.WriteJSON(w, apierror.New(http.StatusBadRequest, "badRequest", "Неверный запрос"))
		return
	}

	payload, err := h.userService.ResetPasswordWithToken(r.Context(), body.Token, body.Password)
	if err != nil {
		apierror.WriteJSON(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *UserHandler) UpdateBio(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)

	var body struct {
		Bio string `json:"bio"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	_ = h.userService.UpdateBio(r.Context(), user.ID, body.Bio)
	user.Bio = body.Bio

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(service.ToPublicUser(user))
}

func (h *UserHandler) ChangeName(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)

	var body struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		apierror.WriteJSON(w, apierror.New(http.StatusBadRequest, "badRequest", "Неверный запрос"))
		return
	}

	updated, err := h.userService.ChangeName(r.Context(), user, body.Name, body.Password)
	if err != nil {
		apierror.WriteJSON(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(updated)
}

func (h *UserHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)

	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		apierror.WriteJSON(w, apierror.New(http.StatusBadRequest, "badRequest", "Неверный запрос"))
		return
	}

	newToken, err := h.userService.ChangePassword(r.Context(), user.ID, body.Current, body.New)
	if err != nil {
		apierror.WriteJSON(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"token": newToken})
}

func (h *UserHandler) UploadAvatar(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)

	data, err := io.ReadAll(r.Body)
	if err != nil {
		apierror.WriteJSON(w, apierror.New(http.StatusBadRequest, "badRequest", "Не удалось прочитать тело запроса"))
		return
	}

	updated, err := h.userService.UpdateAvatar(r.Context(), user, data)
	if err != nil {
		apierror.WriteJSON(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(updated)
}

func (h *UserHandler) DeleteAvatar(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)

	updated, err := h.userService.DeleteAvatar(r.Context(), user)
	if err != nil {
		apierror.WriteJSON(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(updated)
}

func (h *UserHandler) GetAvatar(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	fullPath := filepath.Join(h.avatarDir, name)

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, fullPath)
}

func (h *UserHandler) RemoveUser(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	targetID := chi.URLParam(r, "userID")

	if err := h.userService.SetUserRemoved(r.Context(), user, targetID, true); err != nil {
		apierror.WriteJSON(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *UserHandler) LetBackUser(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	targetID := chi.URLParam(r, "userID")

	if err := h.userService.SetUserRemoved(r.Context(), user, targetID, false); err != nil {
		apierror.WriteJSON(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *UserHandler) BlockUser(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	otherID := chi.URLParam(r, "otherID")

	_ = h.userService.DB.BlockUser(r.Context(), user.ID, otherID)
	blocked, _ := h.userService.DB.GetBlockedIDs(r.Context(), user.ID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(blocked)
}

func (h *UserHandler) UnblockUser(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	otherID := chi.URLParam(r, "otherID")

	_ = h.userService.DB.UnblockUser(r.Context(), user.ID, otherID)
	blocked, _ := h.userService.DB.GetBlockedIDs(r.Context(), user.ID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(blocked)
}

func (h *UserHandler) LogoutAll(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)
	_ = h.userService.DB.InvalidateAllSessions(r.Context(), user.ID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (h *UserHandler) SetStatus(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)

	var body struct {
		Status  string `json:"status"`
		Text    string `json:"text"`
		Minutes *int   `json:"minutes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	var until *int64
	if body.Minutes != nil && *body.Minutes > 0 {
		u := database.NowMs() + int64(*body.Minutes)*60_000
		until = &u
	}

	_ = h.userService.DB.UpdateStatus(r.Context(), user.ID, body.Status, body.Text, until)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": body.Status,
		"text":   body.Text,
		"until":  until,
	})
}

func (h *UserHandler) Bootstrap(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*domain.User)

	servers, _ := h.serverService.ListUserServers(r.Context(), user)
	blocked, _ := h.userService.DB.GetBlockedIDs(r.Context(), user.ID)

	resp := map[string]any{
		"me":               service.ToPublicUser(user),
		"isSuperadmin":     user.IsSuperadmin,
		"canCreateServers": user.MayCreateServer(),
		"servers":          servers,
		"blocked":          blocked,
		"livekitUrl":       h.userService.Cfg.LiveKitURL,
		"serverName":       h.userService.Cfg.InstanceName,
		"relayOnly":        h.userService.Cfg.RelayOnly,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
