package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/Mrprogramist/echo/internal/auth"
	"github.com/Mrprogramist/echo/internal/config"
	"github.com/Mrprogramist/echo/internal/database"
	"github.com/Mrprogramist/echo/internal/realtime"
	"github.com/Mrprogramist/echo/internal/service"
	transporthttp "github.com/Mrprogramist/echo/internal/transport/http"
)

func main() {
	log.Println("Starting Atrium Server (Go edition)...")

	// 1. Конфигурация (.env / окружение)
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}
	log.Printf("Loaded config for instance: %s [Env: %s]", cfg.InstanceName, cfg.Env)

	// 2. База данных SQLite
	db, err := database.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()
	log.Printf("SQLite database ready at: %s", cfg.DBPath)

	if len(cfg.Superadmins) > 0 {
		_ = db.SeedSuperadmins(context.Background(), cfg.Superadmins)
	}

	// 3. Менеджеры безопасности и LiveKit
	authMgr := auth.NewAuthManager(cfg, db)
	livekitMgr := realtime.NewLiveKitManager(cfg, db)
	middleware := transporthttp.NewMiddleware(authMgr, cfg)

	// 4. Сервисный слой
	instanceSvc := service.NewInstanceService(cfg, db)
	serverSvc := service.NewServerService(cfg, db)
	channelSvc := service.NewChannelService(cfg, db, serverSvc)
	dmSvc := service.NewDMService(cfg, db)
	uploadSvc := service.NewUploadService(cfg, db, serverSvc)
	userSvc := service.NewUserService(cfg, db)
	discordImportSvc := service.NewDiscordImportService(db, serverSvc)

	// 5. Фоновые процессы (БЕЗ ping_loop телеметрии)
	bgCtx, cancelBg := context.WithCancel(context.Background())
	defer cancelBg()

	userSvc.StartStatusExpiryLoop(bgCtx)
	uploadSvc.StartHousekeepingLoop(bgCtx)

	// 6. Роутер Chi
	r := chi.NewRouter()

	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Logger)
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.Timeout(60 * time.Second))

	// CORS для работы веб-клиента
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-CSRF-Token")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusOK)
				return
			}
			next.ServeHTTP(w, r)
		})
	})

	// Регистрация всех доменных API маршрутов
	transporthttp.NewInstanceHandler(instanceSvc).RegisterRoutes(r, middleware.AuthRequired)
	transporthttp.NewServerHandler(serverSvc).RegisterRoutes(r, middleware.AuthRequired)
	transporthttp.NewChannelHandler(channelSvc).RegisterRoutes(r, middleware.AuthRequired)
	transporthttp.NewDMHandler(dmSvc).RegisterRoutes(r, middleware.AuthRequired)
	transporthttp.NewUploadHandler(uploadSvc, cfg.UploadDir).RegisterRoutes(r, middleware.AuthRequired, middleware.MediaAuthRequired)
	transporthttp.NewUserHandler(userSvc, serverSvc, cfg.AvatarDir).RegisterRoutes(r, middleware.AuthRequired)
	transporthttp.NewDiscordImportHandler(discordImportSvc).RegisterRoutes(r, middleware.AuthRequired)
	transporthttp.NewRTCHandler(livekitMgr, serverSvc, dmSvc).RegisterRoutes(r, middleware.AuthRequired)

	// 7. Раздача статики фронтенда (React SPA) с правильным Vite-кэшированием из app.py
	if cfg.WebDistPath != "" {
		fs := http.FileServer(http.Dir(cfg.WebDistPath))

		r.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// Ассеты Vite с хэшами в именах кэшируем на 1 год, остальное (включая index.html) — no-cache
			if strings.HasPrefix(req.URL.Path, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}

			// SPA Fallback: если запрошенный путь отсутствует на диске — отдаем index.html
			fullPath := filepath.Join(cfg.WebDistPath, filepath.Clean(req.URL.Path))
			if _, err := os.Stat(fullPath); os.IsNotExist(err) {
				http.ServeFile(w, req, filepath.Join(cfg.WebDistPath, "index.html"))
				return
			}

			fs.ServeHTTP(w, req)
		}))
	}

	// 8. Запуск сервера
	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}
	serverAddr := fmt.Sprintf("0.0.0.0:%s", port)

	server := &http.Server{
		Addr:         serverAddr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("Server listening on http://%s", serverAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server ListenAndServe error: %v", err)
		}
	}()

	// Graceful Shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server gracefully...")
	cancelBg()

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited cleanly.")
}
