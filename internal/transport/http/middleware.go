package http

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/Mrprogramist/echo/internal/apierror"
	"github.com/Mrprogramist/echo/internal/auth"
	"github.com/Mrprogramist/echo/internal/config"
	"github.com/Mrprogramist/echo/internal/domain"
)

type ContextKey string

const UserContextKey ContextKey = "user"

type Middleware struct {
	authMgr *auth.AuthManager
	cfg     *config.Config
}

func NewMiddleware(authMgr *auth.AuthManager, cfg *config.Config) *Middleware {
	return &Middleware{authMgr: authMgr, cfg: cfg}
}

// ClientIP безопасно извлекает IP-адрес клиента (учитывая X-Forwarded-For от Caddy)
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

// AuthRequired проверяет Bearer-токен (scope: "api")
func (m *Middleware) AuthRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		token := strings.TrimPrefix(authHeader, "Bearer ")
		token = strings.TrimSpace(token)

		user, err := m.authMgr.ParseAndValidateToken(r.Context(), token, "api")
		if err != nil {
			apierror.WriteJSON(w, apierror.New(http.StatusUnauthorized, "sessionExpired", "Нужно войти заново"))
			return
		}

		ctx := context.WithValue(r.Context(), UserContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// MediaAuthRequired проверяет HttpOnly куку gx_media (scope: "media")
func (m *Middleware) MediaAuthRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(config.MediaCookieName)
		if err != nil || cookie.Value == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		user, err := m.authMgr.ParseAndValidateToken(r.Context(), cookie.Value, "media")
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), UserContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// SetMediaCookie устанавливает безопасную куку для загрузки изображений в <img>
func (m *Middleware) SetMediaCookie(w http.ResponseWriter, r *http.Request, user *domain.User) {
	token, err := m.authMgr.IssueToken(user.ID, "media", user.TokenVersion)
	if err != nil {
		return
	}

	isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name:     config.MediaCookieName,
		Value:    token,
		Path:     "/api/",
		MaxAge:   int(config.TokenTTL.Seconds()),
		HttpOnly: true,
		Secure:   isSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearMediaCookie очищает куку при выходе
func (m *Middleware) ClearMediaCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     config.MediaCookieName,
		Value:    "",
		Path:     "/api/",
		MaxAge:   -1,
		HttpOnly: true,
	})
}
