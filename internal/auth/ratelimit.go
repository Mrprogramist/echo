package auth

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/Mrprogramist/echo/internal/apierror"
)

type RateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string][]time.Time
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		limit:  limit,
		window: window,
		hits:   make(map[string][]time.Time),
	}
}

// Check проверяет лимит. Если лимит превышен, возвращает HTTP 429 ошибку
func (rl *RateLimiter) Check(key string) error {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// Периодическая очистка от старых ключей (защита от утечки RAM)
	if len(rl.hits) > 10000 {
		cutoff := now.Add(-rl.window)
		for k, timestamps := range rl.hits {
			if len(timestamps) == 0 || timestamps[len(timestamps)-1].Before(cutoff) {
				delete(rl.hits, k)
			}
		}
	}

	cutoff := now.Add(-rl.window)
	timestamps := rl.hits[key]

	// Удаляем устаревшие метки времени
	validIdx := 0
	for i, t := range timestamps {
		if t.After(cutoff) {
			validIdx = i
			break
		}
		if i == len(timestamps)-1 {
			validIdx = len(timestamps)
		}
	}
	timestamps = timestamps[validIdx:]

	if len(timestamps) >= rl.limit {
		oldest := timestamps[0]
		retryAfter := int(oldest.Add(rl.window).Sub(now).Seconds()) + 1
		if retryAfter < 1 {
			retryAfter = 1
		}

		waitStr := fmt.Sprintf("%d с", retryAfter)
		if retryAfter >= 60 {
			waitStr = fmt.Sprintf("%d мин", (retryAfter+59)/60)
		}

		return apierror.New(http.StatusTooManyRequests, "tooManyTries",
			fmt.Sprintf("Слишком много попыток, повтор через %s", waitStr),
			map[string]any{"seconds": retryAfter, "minutes": (retryAfter + 59) / 60},
		)
	}

	rl.hits[key] = append(timestamps, now)
	return nil
}
