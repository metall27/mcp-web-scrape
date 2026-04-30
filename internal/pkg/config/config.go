package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server    ServerConfig    `mapstructure:"server"`
	MCP       MCPConfig       `mapstructure:"mcp"`
	Scraping  ScrapingConfig  `mapstructure:"scraping"`
	Search    SearchConfig    `mapstructure:"search"`
	RateLimit RateLimitConfig `mapstructure:"rate_limit"`
	Cache     CacheConfig     `mapstructure:"cache"`
	Log       LogConfig       `mapstructure:"log"`
}

type ServerConfig struct {
	Host         string        `mapstructure:"host"`
	Port         int           `mapstructure:"port"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
}

type MCPConfig struct {
	Endpoint    string   `mapstructure:"endpoint"`
	APIKey      string   `mapstructure:"api_key"`
	APIKeyHeader string  `mapstructure:"api_key_header"`
}

type ScrapingConfig struct {
	UserAgent        string        `mapstructure:"user_agent"`
	Timeout          time.Duration `mapstructure:"timeout"`
	MaxRedirects     int           `mapstructure:"max_redirects"`
	MaxBodySize      int64         `mapstructure:"max_body_size"`
	AllowedDomains   []string      `mapstructure:"allowed_domains"`
}

type SearchConfig struct {
	Provider       string `mapstructure:"provider"` // brave, bing, duckduckgo
	APIKey         string `mapstructure:"api_key"`
	MaxResults     int    `mapstructure:"max_results"`
	SafeSearch     bool   `mapstructure:"safe_search"`
}

type RateLimitConfig struct {
	RequestsPerSecond float64 `mapstructure:"requests_per_second"`
	BurstSize         int     `mapstructure:"burst_size"`
	Enabled           bool    `mapstructure:"enabled"`
}

type CacheConfig struct {
	Enabled    bool          `mapstructure:"enabled"`
	TTL        time.Duration `mapstructure:"ttl"`
	CleanupInt time.Duration `mapstructure:"cleanup_interval"`
}

type LogConfig struct {
	Level  string `mapstructure:"level"`
	Pretty bool   `mapstructure:"pretty"`
}

func Load(configPath string) (*Config, error) {
	v := viper.New()

	// Set defaults
	setDefaults(v)

	// Read config file
	if configPath != "" {
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	// Environment variables
	v.SetEnvPrefix("MCP_WEB_SCRAPE")
	v.AutomaticEnv()

	// Unmarshal
	config := &Config{}
	if err := v.Unmarshal(config); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	return config, nil
}

func setDefaults(v *viper.Viper) {
	// Server defaults
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.read_timeout", 30*time.Second)
	v.SetDefault("server.write_timeout", 30*time.Second)

	// MCP defaults
	v.SetDefault("mcp.endpoint", "/mcp")
	v.SetDefault("mcp.api_key_header", "X-API-Key")

	// Scraping defaults
	v.SetDefault("scraping.user_agent", "MCP-Web-Scrape/1.0")
	v.SetDefault("scraping.timeout", 30*time.Second)
	v.SetDefault("scraping.max_redirects", 10)
	v.SetDefault("scraping.max_body_size", 10*1024*1024) // 10MB

	// Search defaults
	v.SetDefault("search.provider", "duckduckgo")
	v.SetDefault("search.max_results", 10)
	v.SetDefault("search.safe_search", true)

	// Rate limiting defaults
	v.SetDefault("rate_limit.requests_per_second", 10.0)
	v.SetDefault("rate_limit.burst_size", 20)
	v.SetDefault("rate_limit.enabled", true)

	// Cache defaults
	v.SetDefault("cache.enabled", true)
	v.SetDefault("cache.ttl", 5*time.Minute)
	v.SetDefault("cache.cleanup_interval", 10*time.Minute)

	// Log defaults
	v.SetDefault("log.level", "info")
	v.SetDefault("log.pretty", true)
}
