package domain

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/rs/zerolog"
	"gopkg.in/yaml.v3"
)

// DomainStats хранит статистику по конкретному домену
type DomainStats struct {
	Domain          string    `yaml:"domain"`
	PreferredMethod string    `yaml:"preferred_method"` // "HTTP" or "Chrome"
	DeterminedAt    time.Time `yaml:"determined_at"`
	SuccessCount    int       `yaml:"success_count"`
	FailureCount    int       `yaml:"failure_count"`
	LastAttempt     time.Time `yaml:"last_attempt"`
}

// MethodLearner управляет статистикой методов по доменам
type MethodLearner struct {
	config   Config
	domains  map[string]*DomainStats // domain -> stats
	mutex    sync.RWMutex
	storageFile string
	logger   zerolog.Logger
}

// Config конфигурация MethodLearner
type Config struct {
	Enabled    bool
	StorageDir string
}

// NewMethodLearner создает новый MethodLearner
func NewMethodLearner(config Config) *MethodLearner {
	ml := &MethodLearner{
		config:     config,
		domains:    make(map[string]*DomainStats),
		storageFile: filepath.Join(config.StorageDir, "site_methods.yaml"),
		logger:     logger.Get(),
	}

	if config.Enabled {
		// Создаем директорию если не существует
		if err := os.MkdirAll(config.StorageDir, 0755); err != nil {
			ml.logger.Error().
				Err(err).
				Str("storage_dir", config.StorageDir).
				Msg("Failed to create storage directory, site method learning disabled")
			ml.config.Enabled = false
			return ml
		}

		// Загружаем существующую статистику
		if err := ml.Load(); err != nil {
			ml.logger.Warn().
				Err(err).
				Msg("Failed to load site methods, starting with empty state")
		} else {
			ml.logger.Info().
				Int("domains_count", len(ml.domains)).
				Msg("Loaded site method statistics")
		}
	}

	return ml
}

// GetPreferredMethod возвращает предпочитаемый метод для домена (если есть достаточно данных)
func (ml *MethodLearner) GetPreferredMethod(domain string) (string, bool) {
	if !ml.config.Enabled {
		return "", false
	}

	ml.mutex.RLock()
	defer ml.mutex.RUnlock()

	stats, exists := ml.domains[domain]
	if !exists {
		return "", false // Нет данных
	}

	// Проверяем что данные актуальны (не старше 7 дней)
	if time.Since(stats.DeterminedAt) > 7*24*time.Hour {
		return "", false // Данные устарели
	}

	// Возвращаем предпочитаемый метод если есть достаточно успехов
	if stats.SuccessCount >= 3 {
		return stats.PreferredMethod, true
	}

	return "", false // Недостаточно данных
}

// RecordSuccess записывает успешный scrape для домена
func (ml *MethodLearner) RecordSuccess(domain, method string) {
	if !ml.config.Enabled {
		return
	}

	ml.mutex.Lock()
	defer ml.mutex.Unlock()

	stats := ml.getOrCreateStats(domain)
	stats.SuccessCount++
	stats.LastAttempt = time.Now()

	// Если это первая успешная попытка или метод изменился
	if stats.PreferredMethod == "" || stats.SuccessCount == 1 {
		stats.PreferredMethod = method
		stats.DeterminedAt = time.Now()
		ml.logger.Info().
			Str("domain", domain).
			Str("method", method).
			Msg("Recording first successful method for domain")
	}

	// Сохраняем при каждом обновлении
	go ml.Save() // Асинхронно сохраняем
}

// RecordFailure записывает failed scrape для домена
func (ml *MethodLearner) RecordFailure(domain, method string) {
	if !ml.config.Enabled {
		return
	}

	ml.mutex.Lock()
	defer ml.mutex.Unlock()

	stats := ml.getOrCreateStats(domain)
	stats.FailureCount++
	stats.LastAttempt = time.Now()

	// Если failure rate слишком высокий, сбрасываем preferred method
	totalAttempts := stats.SuccessCount + stats.FailureCount
	if totalAttempts > 5 {
		failureRate := float64(stats.FailureCount) / float64(totalAttempts)
		if failureRate > 0.7 { // Если > 70% failures
			ml.logger.Warn().
				Str("domain", domain).
				Str("previous_method", stats.PreferredMethod).
				Float64("failure_rate", failureRate).
				Msg("High failure rate for preferred method, resetting")
			stats.PreferredMethod = ""
			stats.DeterminedAt = time.Time{}
			stats.SuccessCount = 0
			stats.FailureCount = 0
		}
	}

	go ml.Save() // Асинхронно сохраняем
}

// getOrCreateStats получает или создает статистику для домена
func (ml *MethodLearner) getOrCreateStats(domain string) *DomainStats {
	if stats, exists := ml.domains[domain]; exists {
		return stats
	}

	stats := &DomainStats{
		Domain:       domain,
		LastAttempt:  time.Now(),
	}
	ml.domains[domain] = stats
	return stats
}

// Save сохраняет статистику в YAML файл
func (ml *MethodLearner) Save() error {
	if !ml.config.Enabled {
		return nil
	}

	ml.mutex.RLock()
	defer ml.mutex.RUnlock()

	// Готовим данные для сохранения
	data := struct {
		Domains map[string]*DomainStats `yaml:"domains"`
	}{
		Domains: ml.domains,
	}

	// Marshal to YAML
	yamlData, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal site methods: %w", err)
	}

	// Write to file
	if err := os.WriteFile(ml.storageFile, yamlData, 0644); err != nil {
		return fmt.Errorf("failed to write site methods file: %w", err)
	}

	ml.logger.Debug().
		Int("domains_count", len(ml.domains)).
		Str("file", ml.storageFile).
		Msg("Saved site method statistics")

	return nil
}

// Load загружает статистику из YAML файла
func (ml *MethodLearner) Load() error {
	if !ml.config.Enabled {
		return nil
	}

	ml.mutex.Lock()
	defer ml.mutex.Unlock()

	// Read file
	data, err := os.ReadFile(ml.storageFile)
	if err != nil {
		if os.IsNotExist(err) {
			// Файл не существует - это нормально при первом запуске
			return nil
		}
		return err
	}

	// Unmarshal from YAML
	var yamlData struct {
		Domains map[string]*DomainStats `yaml:"domains"`
	}
	if err := yaml.Unmarshal(data, &yamlData); err != nil {
		return err
	}

	// Load domains
	ml.domains = yamlData.Domains
	if ml.domains == nil {
		ml.domains = make(map[string]*DomainStats)
	}

	return nil
}

// GetStats возвращает статистику по домену (для дебага)
func (ml *MethodLearner) GetStats(domain string) (*DomainStats, bool) {
	ml.mutex.RLock()
	defer ml.mutex.RUnlock()

	stats, exists := ml.domains[domain]
	return stats, exists
}

// GetAllStats возвращает статистику по всем доменам (для дебага)
func (ml *MethodLearner) GetAllStats() map[string]*DomainStats {
	ml.mutex.RLock()
	defer ml.mutex.RUnlock()

	// Делаем копию чтобы избежать race conditions
	result := make(map[string]*DomainStats, len(ml.domains))
	for k, v := range ml.domains {
		result[k] = v
	}
	return result
}

// CatalogMethod хранит паттерны каталога для конкретного домена
type CatalogMethod struct {
	Domain            string    `yaml:"domain"`
	CatalogPaths      []string  `yaml:"catalog_paths"`       // Обнаруженные пути к каталогам
	ProductPattern    string    `yaml:"product_pattern"`     // CSS паттерн для карточек продуктов
	PricePattern      string    `yaml:"price_pattern"`       // Паттерн для извлечения цен
	PaginationPattern string    `yaml:"pagination_pattern"`  // Паттерн для навигации по страницам
	SuccessRate       float64   `yaml:"success_rate"`        // Успешность применения паттернов
	LastSeen          time.Time `yaml:"last_seen"`           // Когда последний раз использовался
}

// CatalogPatterns хранит паттерны для всех доменов
type CatalogPatterns struct {
	Domains map[string]*CatalogMethod `yaml:"domains"`
}

// SaveCatalogPattern сохраняет паттерны каталога для домена
func (ml *MethodLearner) SaveCatalogPattern(domain string, pattern CatalogMethod) error {
	if !ml.config.Enabled {
		return nil // Если метод learning отключен, просто возвращаем успех
	}

	ml.mutex.Lock()
	defer ml.mutex.Unlock()

	// Загружаем существующие паттерны
	patterns := &CatalogPatterns{
		Domains: make(map[string]*CatalogMethod),
	}

	// Пробуем загрузить существующий файл
	if data, err := os.ReadFile(ml.storageFile); err == nil {
		var existingPatterns CatalogPatterns
		if err := yaml.Unmarshal(data, &existingPatterns); err == nil {
			patterns.Domains = existingPatterns.Domains
		}
	}

	// Добавляем/обновляем паттерн для домена
	pattern.Domain = domain
	pattern.LastSeen = time.Now()
	patterns.Domains[domain] = &pattern

	// Сохраняем в файл
	data, err := yaml.Marshal(patterns)
	if err != nil {
		ml.logger.Error().Err(err).Str("domain", domain).Msg("Failed to marshal catalog patterns")
		return fmt.Errorf("failed to marshal catalog patterns: %w", err)
	}

	// Сохраняем в специальный файл для catalog patterns
	catalogStorageFile := filepath.Join(ml.config.StorageDir, "catalog_patterns.yaml")
	if err := os.WriteFile(catalogStorageFile, data, 0644); err != nil {
		ml.logger.Error().Err(err).Str("domain", domain).Msg("Failed to save catalog patterns")
		return fmt.Errorf("failed to save catalog patterns: %w", err)
	}

	ml.logger.Info().Str("domain", domain).Msg("Successfully saved catalog patterns")
	return nil
}

// LoadCatalogPattern загружает сохраненные паттерны для домена
func (ml *MethodLearner) LoadCatalogPattern(domain string) (CatalogMethod, bool) {
	if !ml.config.Enabled {
		return CatalogMethod{}, false
	}

	ml.mutex.RLock()
	defer ml.mutex.RUnlock()

	// Загружаем файл паттернов
	catalogStorageFile := filepath.Join(ml.config.StorageDir, "catalog_patterns.yaml")
	data, err := os.ReadFile(catalogStorageFile)
	if err != nil {
		ml.logger.Debug().Err(err).Str("domain", domain).Msg("No catalog patterns file found")
		return CatalogMethod{}, false
	}

	// Парсим YAML
	var patterns CatalogPatterns
	if err := yaml.Unmarshal(data, &patterns); err != nil {
		ml.logger.Error().Err(err).Str("domain", domain).Msg("Failed to parse catalog patterns")
		return CatalogMethod{}, false
	}

	// Получаем паттерн для домена
	pattern, exists := patterns.Domains[domain]
	if !exists {
		ml.logger.Debug().Str("domain", domain).Msg("No catalog patterns found for domain")
		return CatalogMethod{}, false
	}

	ml.logger.Debug().Str("domain", domain).Msg("Successfully loaded catalog patterns")
	return *pattern, true
}

// GetAllCatalogPatterns возвращает все сохраненные паттерны каталогов
func (ml *MethodLearner) GetAllCatalogPatterns() map[string]CatalogMethod {
	if !ml.config.Enabled {
		return make(map[string]CatalogMethod)
	}

	ml.mutex.RLock()
	defer ml.mutex.RUnlock()

	// Загружаем файл паттернов
	catalogStorageFile := filepath.Join(ml.config.StorageDir, "catalog_patterns.yaml")
	data, err := os.ReadFile(catalogStorageFile)
	if err != nil {
		ml.logger.Debug().Err(err).Msg("No catalog patterns file found")
		return make(map[string]CatalogMethod)
	}

	// Парсим YAML
	var patterns CatalogPatterns
	if err := yaml.Unmarshal(data, &patterns); err != nil {
		ml.logger.Error().Err(err).Msg("Failed to parse catalog patterns")
		return make(map[string]CatalogMethod)
	}

	// Конвертируем в обычный map для безопасного использования
	result := make(map[string]CatalogMethod, len(patterns.Domains))
	for k, v := range patterns.Domains {
		if v != nil {
			result[k] = *v
		}
	}

	return result
}

// UpdateCatalogSuccessRate обновляет успешность паттернов для домена
func (ml *MethodLearner) UpdateCatalogSuccessRate(domain string, success bool) error {
	if !ml.config.Enabled {
		return nil
	}

	ml.mutex.Lock()
	defer ml.mutex.Unlock()

	// Загружаем текущие паттерны
	pattern, exists := ml.LoadCatalogPattern(domain)
	if !exists {
		// Если паттернов нет, создаем базовые
		pattern = CatalogMethod{
			Domain:      domain,
			SuccessRate: 0.5, // Начальный рейтинг
		}
	}

	// Обновляем успешность (простая экспоненциальная скользящая средняя)
	if success {
		pattern.SuccessRate = pattern.SuccessRate*0.8 + 0.2*1.0
	} else {
		pattern.SuccessRate = pattern.SuccessRate*0.8 + 0.2*0.0
	}

	// Сохраняем обновленные паттерны
	return ml.SaveCatalogPattern(domain, pattern)
}
