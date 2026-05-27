package proxy

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

type Rotator struct {
	proxies      []Proxy
	currentIndex uint64
	enabled      bool
	mu           sync.RWMutex
	logger       zerolog.Logger
	stats        *Stats
}

type Proxy struct {
	URL      string        // Полный URL прокси
	Protocol string        // http, https, socks5
	Host     string        // хост:порт
	Username string        // опционально
	Password string        // опционально
	Timeout  time.Duration // timeout для проверки
}

type Config struct {
	Proxies       []string // Список прокси URLs
	Enabled       bool     // Включить ротацию прокси
	TestOnStartup bool     // Проверить прокси при старте
	TestTimeout   time.Duration
	Logger        zerolog.Logger
}

type Stats struct {
	TotalRequests    uint64
	SuccessfulReqs   uint64
	FailedReqs       uint64
	LastUsedIndex    uint64
	LastError        string
	LastFailureTime  time.Time
}

func New(cfg Config) (*Rotator, error) {
	if len(cfg.Proxies) == 0 {
		cfg.Logger.Info().Msg("No proxies configured, running without proxy")
		return &Rotator{
			enabled: false,
			logger:  cfg.Logger,
			stats:   &Stats{},
		}, nil
	}

	r := &Rotator{
		proxies: make([]Proxy, 0, len(cfg.Proxies)),
		enabled: cfg.Enabled,
		logger:  cfg.Logger,
		stats:   &Stats{},
	}

	// Парсим и валидируем прокси
	for _, proxyURL := range cfg.Proxies {
		proxy, err := r.parseProxy(proxyURL)
		if err != nil {
			r.logger.Warn().
				Str("proxy", proxyURL).
				Err(err).
				Msg("Invalid proxy URL, skipping")
			continue
		}

		r.proxies = append(r.proxies, proxy)
		r.logger.Info().
			Str("proxy", proxy.URL).
			Str("protocol", proxy.Protocol).
			Msg("Added proxy to rotation pool")
	}

	if len(r.proxies) == 0 {
		return nil, fmt.Errorf("no valid proxies found in configuration")
	}

	r.logger.Info().
		Int("total_proxies", len(r.proxies)).
		Bool("enabled", r.enabled).
		Msg("Proxy rotator initialized")

	// Проверяем прокси при старте если включено
	if cfg.TestOnStartup {
		go r.testProxies(cfg.TestTimeout)
	}

	return r, nil
}

// parseProxy парсит URL прокси
func (r *Rotator) parseProxy(proxyURL string) (Proxy, error) {
	// Добавляем схему если отсутствует
	if !containsScheme(proxyURL) {
		proxyURL = "http://" + proxyURL
	}

	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return Proxy{}, fmt.Errorf("invalid proxy URL: %w", err)
	}

	// Проверяем схему
	switch parsed.Scheme {
	case "http", "https", "socks5":
		// OK
	default:
		return Proxy{}, fmt.Errorf("unsupported proxy scheme: %s (supported: http, https, socks5)", parsed.Scheme)
	}

	proxy := Proxy{
		URL:      parsed.String(),
		Protocol: parsed.Scheme,
		Host:     parsed.Host,
		Username: parsed.User.Username(),
		Timeout:  10 * time.Second,
	}

	if parsed.User != nil {
		proxy.Password, _ = parsed.User.Password()
	}

	return proxy, nil
}

// containsScheme проверяет наличие схемы в URL
func containsScheme(urlStr string) bool {
	for i := 0; i < len(urlStr); i++ {
		c := urlStr[i]
		if c == ':' {
			if i+2 < len(urlStr) && urlStr[i+1] == '/' && urlStr[i+2] == '/' {
				return true
			}
			return false
		}
		if c == '/' || c == '#' {
			return false
		}
	}
	return false
}

// GetNext возвращает следующий прокси по round-robin
func (r *Rotator) GetNext() (*Proxy, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.enabled || len(r.proxies) == 0 {
		return nil, nil // Прокси отключены или нет прокси
	}

	// Round-robin с atomic counter
	index := atomic.AddUint64(&r.currentIndex, 1) % uint64(len(r.proxies))
	atomic.StoreUint64(&r.stats.LastUsedIndex, index)
	atomic.AddUint64(&r.stats.TotalRequests, 1)

	proxy := &r.proxies[index]

	r.logger.Debug().
		Int("index", int(index)).
		Str("proxy", proxy.URL).
		Msg("Selected proxy for request")

	return proxy, nil
}

// GetRandom возвращает случайный прокси
func (r *Rotator) GetRandom() (*Proxy, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.enabled || len(r.proxies) == 0 {
		return nil, nil
	}

	// Используем time для рандома (вместо math/rand для thread-safety)
	nano := time.Now().UnixNano()
	index := int(nano % int64(len(r.proxies)))
	atomic.StoreUint64(&r.stats.LastUsedIndex, uint64(index))
	atomic.AddUint64(&r.stats.TotalRequests, 1)

	proxy := &r.proxies[index]

	r.logger.Debug().
		Int("index", index).
		Str("proxy", proxy.URL).
		Msg("Selected random proxy")

	return proxy, nil
}

// MarkSuccess отмечает успешный запрос через прокси
func (r *Rotator) MarkSuccess() {
	atomic.AddUint64(&r.stats.SuccessfulReqs, 1)
}

// MarkFailure отмечает неудачный запрос через прокси
func (r *Rotator) MarkFailure(err error) {
	atomic.AddUint64(&r.stats.FailedReqs, 1)

	if err != nil {
		atomic.StoreUint64(&r.stats.LastUsedIndex, r.currentIndex)
		r.stats.LastError = err.Error()
		r.stats.LastFailureTime = time.Now()
	}
}

// IsEnabled проверяет включена ли ротация прокси
func (r *Rotator) IsEnabled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.enabled && len(r.proxies) > 0
}

// GetCount возвращает количество прокси
func (r *Rotator) GetCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.proxies)
}

// GetAll возвращает копию списка всех прокси
func (r *Rotator) GetAll() []Proxy {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Proxy, len(r.proxies))
	copy(result, r.proxies)
	return result
}

// GetStats возвращает статистику использования
func (r *Rotator) GetStats() Stats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return Stats{
		TotalRequests:   atomic.LoadUint64(&r.stats.TotalRequests),
		SuccessfulReqs:  atomic.LoadUint64(&r.stats.SuccessfulReqs),
		FailedReqs:      atomic.LoadUint64(&r.stats.FailedReqs),
		LastUsedIndex:   atomic.LoadUint64(&r.stats.LastUsedIndex),
		LastError:       r.stats.LastError,
		LastFailureTime: r.stats.LastFailureTime,
	}
}

// testProxies проверяет доступность прокси
func (r *Rotator) testProxies(timeout time.Duration) {
	r.logger.Info().
		Int("count", len(r.proxies)).
		Dur("timeout", timeout).
		Msg("Testing proxies...")

	for i, proxy := range r.proxies {
		go func(index int, p Proxy) {
			err := r.testSingleProxy(p, timeout)
			if err != nil {
				r.logger.Warn().
					Int("index", index).
					Str("proxy", p.URL).
					Err(err).
					Msg("Proxy test failed")
			} else {
				r.logger.Info().
					Int("index", index).
					Str("proxy", p.URL).
					Msg("Proxy test successful")
			}
		}(i, proxy)
	}
}

// testSingleProxy проверяет один прокси
func (r *Rotator) testSingleProxy(proxy Proxy, timeout time.Duration) error {
	// Создаем тестовое соединение
	conn, err := net.DialTimeout("tcp", proxy.Host, timeout)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer conn.Close()

	return nil
}

// GetProxyFunc возвращает функцию для http.Transport.Proxy
func (r *Rotator) GetProxyFunc() func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		if !r.IsEnabled() {
			return nil, nil // Использовать системные настройки
		}

		proxy, err := r.GetNext()
		if err != nil {
			return nil, err
		}

		if proxy == nil {
			return nil, nil // Прокси отключены
		}

		parsedURL, err := url.Parse(proxy.URL)
		if err != nil {
			r.logger.Error().
				Str("proxy", proxy.URL).
				Err(err).
				Msg("Failed to parse proxy URL")
			return nil, err
		}

		return parsedURL, nil
	}
}

// String возвращает строковое представление прокси для chromedp
func (p *Proxy) String() string {
	return p.URL
}

// ToSocks5Proxy конвертирует в формат socks5 для chromedp
func (p *Proxy) ToSocks5Proxy() string {
	if p.Protocol == "socks5" {
		return p.Host
	}
	return ""
}
