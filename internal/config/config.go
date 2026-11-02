package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Константы лимитов предметной области
const (
	TokenTTL        = 180 * 24 * time.Hour // Время жизни сессии (180 дней)
	VoiceTokenTTL   = 10 * time.Minute     // Время жизни LiveKit-токена
	MaxMessageLen   = 4000
	MinPasswordLen  = 8
	MaxGroupMembers = 10
	RingTimeout     = 45 * time.Second
	MediaCookieName = "gx_media"
	MaxEmojiLen     = 32
	MaxAttachments  = 10
	MaxFileNameLen  = 120
	UnsentUploadTTL = 24 * time.Hour

	// Лимиты загрузок и графики (защита от DoS / исчерпания RAM)
	MaxAvatarUploadBytes = 8 * 1024 * 1024    // 8 MB
	AvatarSize           = 256                // 256x256 px
	ServerIconSize       = 512                // 512x512 px
	MaxImageUploadBytes  = 20 * 1024 * 1024   // 20 MB
	MaxFileUploadBytes   = 25 * 1024 * 1024   // 25 MB
	ImageMaxSide         = 2560               // Макс. сторона картинки
	PreviewSide          = 960                // Сторона превью в чате
	AnimationMaxSide     = 800                // Макс. сторона анимированного GIF/WebP
	MaxImagePixels       = 40_000_000         // Защита от декомпрессионных бомб (40 MPix)
	AnimationPixelBudget = 40_000_000         // Суммарный бюджет пикселей по всем кадрам
	MinFreeDiskBytes     = 1024 * 1024 * 1024 // 1 GB свободного места на диске

	// Алфавит для кодов инвайтов без похожих символов (0/O, 1/I/L)
	InviteAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
)

// Цвета аватарок пользователей по умолчанию
var UserColors = []string{
	"#5865f2", "#3ba55c", "#faa61a", "#ed4245",
	"#eb459e", "#1abc9c", "#9b59b6", "#e67e22",
}

// Регулярное выражение для поиска упоминаний (<@32-hex-id> или <@all>)
var MentionRegex = regexp.MustCompile(`<@([0-9a-fA-F]{32}|all)>`)

// Каналы по умолчанию при создании нового сервера
var DefaultServerChannels = [][2]string{
	{"общий", "text"},
	{"голосовой", "voice"},
}

type Config struct {
	Env         string // "development" или "production"
	WebDistPath string // Путь к статике фронтенда

	InstanceName string // Название платформы
	DataDir      string
	DBPath       string
	AvatarDir    string
	UploadDir    string

	AppSecret   string // Секретный ключ JWT
	InviteCode  string // Bootstrap-код первого суперадмина
	Superadmins []string

	AllowPublicRegistration bool  // Разрешена ли открытая регистрация
	DefaultCanCreateServers bool  // Могут ли новые пользователи создавать серверы
	InviteLimit             int   // Лимит инвайтов
	UploadBytesPerDay       int64 // Квота в байтах на сутки
	GuestMode               bool  // Демо-режим гостя

	LiveKitAPIKey    string
	LiveKitAPISecret string
	LiveKitURL       string // Внешний WSS адрес
	LiveKitAPI       string // Внутренний HTTP адрес
	RelayOnly        bool
}

// Load считывает .env файл (если есть) и возвращает заполненную структуру Config
func Load() (*Config, error) {
	// 1. Пытаемся загрузить .env файл перед чтением переменных
	envFile := getEnv("ENV_FILE", ".env")
	_ = loadDotEnv(envFile)

	// 2. Читаем переменные окружения
	dataDir := getEnv("DATA_DIR", "./data")
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("invalid DATA_DIR: %w", err)
	}

	cfg := &Config{
		Env:         getEnv("ENV", "development"),
		WebDistPath: os.Getenv("WEB_DIST"),

		InstanceName: getEnv("INSTANCE_NAME", getEnv("SERVER_NAME", "Atrium")),
		DataDir:      absDataDir,
		DBPath:       filepath.Join(absDataDir, "hangout.db"),
		AvatarDir:    filepath.Join(absDataDir, "avatars"),
		UploadDir:    filepath.Join(absDataDir, "uploads"),

		AppSecret:   getEnv("APP_SECRET", "dev-secret-change-me"),
		InviteCode:  getEnv("INVITE_CODE", "friends"),
		Superadmins: parseCommaSeparated(os.Getenv("SUPERADMINS")),

		AllowPublicRegistration: getEnvBool("ALLOW_PUBLIC_REGISTRATION", false),
		DefaultCanCreateServers: getEnvBool("DEFAULT_CAN_CREATE_SERVERS", false),
		InviteLimit:             getEnvInt("INVITE_LIMIT", 5),
		UploadBytesPerDay:       int64(getEnvInt("UPLOAD_MB_PER_DAY", 500)) * 1024 * 1024,
		GuestMode:               getEnvBool("GUEST_MODE", false),

		LiveKitAPIKey:    getEnv("LIVEKIT_API_KEY", "devkey"),
		LiveKitAPISecret: getEnv("LIVEKIT_API_SECRET", "secret"),
		LiveKitURL:       getEnv("LIVEKIT_URL", "ws://localhost:7880"),
		LiveKitAPI:       getEnv("LIVEKIT_API", "http://127.0.0.1:7880"),
		RelayOnly:        getEnvBool("RELAY_ONLY", false),
	}

	if len(cfg.Superadmins) == 0 {
		cfg.Superadmins = parseCommaSeparated(os.Getenv("ADMINS"))
	}

	// 3. Создаем директории на диске
	if err := cfg.ensureDirs(); err != nil {
		return nil, err
	}

	// 4. Проверяем безопасность в продакшне
	if err := cfg.validateSecurity(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// loadDotEnv считывает файл ключ=значение в os.Environ без внешних библиотек
func loadDotEnv(filepath string) error {
	file, err := os.Open(filepath)
	if err != nil {
		return err // Если файла нет — просто тихо продолжаем
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		// Убираем внешние кавычки ("value" или 'value')
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}

		// Устанавливаем только если переменная еще НЕ задана в ОС
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
	return scanner.Err()
}

func (c *Config) ensureDirs() error {
	dirs := []string{c.DataDir, c.AvatarDir, c.UploadDir}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}
	return nil
}

func (c *Config) validateSecurity() error {
	isProduction := c.Env == "production" || c.WebDistPath != ""
	if !isProduction {
		return nil
	}

	if c.AppSecret == "dev-secret-change-me" {
		return fmt.Errorf("FATAL: APP_SECRET must be set to a secure random string in production")
	}
	if c.LiveKitAPISecret == "secret" {
		return fmt.Errorf("FATAL: LIVEKIT_API_SECRET must be changed from default in production")
	}
	if c.InviteCode == "friends" {
		return fmt.Errorf("FATAL: INVITE_CODE must not be left as 'friends' in production")
	}

	return nil
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	valStr := os.Getenv(key)
	if valStr == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(valStr)
	if err != nil {
		return defaultVal
	}
	return val
}

func getEnvBool(key string, defaultVal bool) bool {
	valStr := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if valStr == "" {
		return defaultVal
	}
	return valStr == "1" || valStr == "true" || valStr == "yes"
}

func parseCommaSeparated(val string) []string {
	if strings.TrimSpace(val) == "" {
		return nil
	}
	parts := strings.Split(val, ",")
	res := make([]string, 0, len(parts))
	for _, p := range parts {
		clean := strings.ToLower(strings.TrimSpace(p))
		if clean != "" {
			res = append(res, clean)
		}
	}
	return res
}
