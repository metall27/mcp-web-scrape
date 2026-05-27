package useragent

import (
	"math/rand"
	"sync"
	"time"
)

type Rotator struct {
	userAgents []string
	mu         sync.RWMutex
	rnd        *rand.Rand
}

type Config struct {
	CustomUserAgents []string // Дополнительные UA от пользователя
}

// Список актуальных User-Agent на 2025-2026
// Источники: whatismybrowser.com, useragentstring.com
var defaultUserAgents = []string{
	// Chrome 120-124 (Windows 10/11)
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",

	// Chrome 120-124 (macOS)
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36",

	// Firefox 120-125 (Windows)
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:125.0) Gecko/20100101 Firefox/125.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:124.0) Gecko/20100101 Firefox/124.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:123.0) Gecko/20100101 Firefox/123.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:122.0) Gecko/20100101 Firefox/122.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0",

	// Firefox 120-125 (macOS)
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:125.0) Gecko/20100101 Firefox/125.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:124.0) Gecko/20100101 Firefox/124.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:123.0) Gecko/20100101 Firefox/123.0",

	// Safari 17+ (macOS)
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Safari/605.1.15",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.3 Safari/605.1.15",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Safari/605.1.15",

	// Edge 120-124 (Windows 11)
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36 Edg/124.0.0.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36 Edg/123.0.0.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36 Edg/122.0.0.0",

	// Chrome Mobile (Android) - для мобильных версий сайтов
	"Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.6367.0 Mobile Safari/537.36",
	"Mozilla/5.0 (Linux; Android 13) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Mobile Safari/537.36",

	// Safari Mobile (iOS)
	"Mozilla/5.0 (iPhone; CPU iPhone OS 17_4 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (iPad; CPU OS 17_4 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Mobile/15E148 Safari/604.1",
}

func New(cfg Config) *Rotator {
	r := &Rotator{
		userAgents: make([]string, 0, len(defaultUserAgents) + len(cfg.CustomUserAgents)),
		rnd:        rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	// Добавляем дефолтные UA
	r.userAgents = append(r.userAgents, defaultUserAgents...)

	// Добавляем кастомные UA из конфига
	if len(cfg.CustomUserAgents) > 0 {
		r.userAgents = append(r.userAgents, cfg.CustomUserAgents...)
	}

	return r
}

// Get возвращает случайный User-Agent
func (r *Rotator) Get() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.userAgents) == 0 {
		// Fallback если список пуст (не должно произойти)
		return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	}

	idx := r.rnd.Intn(len(r.userAgents))
	return r.userAgents[idx]
}

// GetForPlatform возвращает случайный UA для конкретной платформы
func (r *Rotator) GetForPlatform(platform string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Фильтруем по платформе
	filtered := make([]string, 0)
	for _, ua := range r.userAgents {
		if containsPlatform(ua, platform) {
			filtered = append(filtered, ua)
		}
	}

	// Если не нашли для платформы, возвращаем любой
	if len(filtered) == 0 {
		idx := r.rnd.Intn(len(r.userAgents))
		return r.userAgents[idx]
	}

	idx := r.rnd.Intn(len(filtered))
	return filtered[idx]
}

// Count возвращает количество доступных UA
func (r *Rotator) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.userAgents)
}

// GetAll возвращает копию списка всех UA (для дебага)
func (r *Rotator) GetAll() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]string, len(r.userAgents))
	copy(result, r.userAgents)
	return result
}

func containsPlatform(ua, platform string) bool {
	uaLower := toLower(ua)
	platformLower := toLower(platform)

	switch platformLower {
	case "windows":
		return contains(uaLower, "windows")
	case "mac", "macos":
		return contains(uaLower, "macintosh") || contains(uaLower, "mac os x")
	case "android":
		return contains(uaLower, "android")
	case "ios":
		return contains(uaLower, "iphone") || contains(uaLower, "ipad")
	case "mobile":
		return contains(uaLower, "mobile") || contains(uaLower, "android") || contains(uaLower, "iphone")
	case "desktop":
		return !contains(uaLower, "mobile") && !contains(uaLower, "android") && !contains(uaLower, "iphone")
	default:
		return true
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsMiddle(s, substr)))
}

func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func toLower(s string) string {
	// Простая реализация для ASCII
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		result[i] = c
	}
	return string(result)
}

// GetRandomDesktop возвращает случайный desktop UA
func (r *Rotator) GetRandomDesktop() string {
	return r.GetForPlatform("desktop")
}

// GetRandomMobile возвращает случайный mobile UA
func (r *Rotator) GetRandomMobile() string {
	return r.GetForPlatform("mobile")
}

// Stats возвращает статистику по UA (для дебага/мониторинга)
func (r *Rotator) Stats() map[string]int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := map[string]int{
		"chrome":     0,
		"firefox":    0,
		"safari":     0,
		"edge":       0,
		"mobile":     0,
		"desktop":    0,
		"total":      len(r.userAgents),
	}

	for _, ua := range r.userAgents {
		uaLower := toLower(ua)
		if contains(uaLower, "chrome") && !contains(uaLower, "edg") {
			stats["chrome"]++
		}
		if contains(uaLower, "firefox") {
			stats["firefox"]++
		}
		if contains(uaLower, "safari") && !contains(uaLower, "chrome") {
			stats["safari"]++
		}
		if contains(uaLower, "edg") {
			stats["edge"]++
		}
		if contains(uaLower, "mobile") || contains(uaLower, "android") || contains(uaLower, "iphone") {
			stats["mobile"]++
		} else {
			stats["desktop"]++
		}
	}

	return stats
}
