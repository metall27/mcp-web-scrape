package http

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	utls "github.com/refraction-networking/utls"
)

// TLSClientConfig настройки для TLS fingerprinting
type TLSClientConfig struct {
	// ChromeHelloID имитирует конкретную версию Chrome
	// Варианты: HelloChrome_100, HelloChrome_106, HelloChrome_120, HelloChrome_131
	ChromeVersion string

	// RandomizeExtensions добавляет рандомизацию порядка расширений
	// для защиты от JA3/JA4 fingerprinting
	RandomizeExtensions bool

	// Timeout для TLS handshake
	HandshakeTimeout time.Duration
}

// DefaultTLSClientConfig дефолтные настройки
var DefaultTLSClientConfig = TLSClientConfig{
	ChromeVersion:       "HelloChrome_120", // Chrome 120 - стабильная версия
	RandomizeExtensions: true,               // Важно для JA4 protection
	HandshakeTimeout:    10 * time.Second,
}

// TLSClient HTTP клиент с TLS fingerprinting
type TLSClient struct {
	config     TLSClientConfig
	httpClient *http.Client
	transport  *http.Transport
}

// NewTLSClient создает новый TLS клиент с fingerprinting
func NewTLSClient(config TLSClientConfig) (*TLSClient, error) {
	if config.ChromeVersion == "" {
		config.ChromeVersion = DefaultTLSClientConfig.ChromeVersion
	}
	if config.HandshakeTimeout == 0 {
		config.HandshakeTimeout = DefaultTLSClientConfig.HandshakeTimeout
	}

	client := &TLSClient{
		config: config,
	}

	// Initialize transport with TLS fingerprinting
	transport := &http.Transport{
		DialTLSContext: client.dialTLSContext,
		// Standard HTTP transport settings
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: config.HandshakeTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		// Enable HTTP/2
		ForceAttemptHTTP2: true,
	}

	client.transport = transport
	client.httpClient = &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	return client, nil
}

// dialTLSContext создает TLS соединение с Chrome fingerprint
func (c *TLSClient) dialTLSContext(ctx context.Context, network, addr string) (net.Conn, error) {
	// Establish TCP connection
	conn, err := net.DialTimeout(network, addr, c.config.HandshakeTimeout)
	if err != nil {
		return nil, fmt.Errorf("TCP dial failed: %w", err)
	}

	// Extract hostname from addr for SNI (remove port)
	// addr format is "hostname:port" or "hostname:443"
	hostname := addr
	if host, _, err := net.SplitHostPort(addr); err == nil {
		hostname = host
	}

	// Parse Chrome version and create ClientHelloID
	helloID := c.getClientHelloID()

	// Create uTLS config with Chrome fingerprint (using hostname only for SNI)
	config := c.createTLSConfig(hostname)

	// Create TLS connection with uTLS
	tlsConn := utls.UClient(conn, config, helloID)

	// Perform TLS handshake with Chrome fingerprint
	if err := tlsConn.Handshake(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("TLS handshake failed: %w", err)
	}

	return tlsConn, nil
}

// getClientHelloID возвращает ClientHelloID для выбранной версии Chrome
func (c *TLSClient) getClientHelloID() utls.ClientHelloID {
	switch c.config.ChromeVersion {
	case "HelloChrome_100":
		return utls.HelloChrome_100
	case "HelloChrome_120":
		return utls.HelloChrome_120
	case "HelloChrome_131":
		return utls.HelloChrome_131
	case "HelloChrome_Auto":
		return utls.HelloChrome_Auto
	default:
		return utls.HelloChrome_120 // Default stable version
	}
}

// createTLSConfig создает TLS конфиг с Chrome fingerprint
func (c *TLSClient) createTLSConfig(serverName string) *utls.Config {
	config := &utls.Config{
		ServerName: serverName,
		// Standard TLS settings
		InsecureSkipVerify: false,
		MinVersion:         tls.VersionTLS12,
		MaxVersion:         tls.VersionTLS13,
		// Chrome-like cipher suite preferences
		CipherSuites: []uint16{
			utls.TLS_AES_128_GCM_SHA256,
			utls.TLS_AES_256_GCM_SHA384,
			utls.TLS_CHACHA20_POLY1305_SHA256,
			utls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			utls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			utls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			utls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			utls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
			utls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
		},
	}

	// Note: Extension randomization is handled internally by uTLS
	// based on the ClientHelloID provided to UClient
	// No need to manually set ExtensionOrder in newer versions

	return config
}

// Do выполняет HTTP запрос с TLS fingerprinting
func (c *TLSClient) Do(req *http.Request) (*http.Response, error) {
	return c.httpClient.Do(req)
}

// Get выполняет GET запрос с TLS fingerprinting
func (c *TLSClient) Get(url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// Post выполняет POST запрос с TLS fingerprinting
func (c *TLSClient) Post(url string, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return c.Do(req)
}

// GetHttpClient возвращает стандартный http.Client для совместимости
func (c *TLSClient) GetHttpClient() *http.Client {
	return c.httpClient
}

// GetTransport возвращает http.Transport для proxy settings
func (c *TLSClient) GetTransport() *http.Transport {
	return c.transport
}

// SetProxy устанавливает proxy для TLS клиента
func (c *TLSClient) SetProxy(proxyURL string) error {
	if proxyURL != "" {
		parsedURL, err := url.Parse(proxyURL)
		if err != nil {
			return fmt.Errorf("failed to parse proxy URL: %w", err)
		}
		c.transport.Proxy = http.ProxyURL(parsedURL)
	} else {
		c.transport.Proxy = http.ProxyFromEnvironment
	}
	return nil
}

// Close закрывает TLS клиент и освобождает ресурсы
func (c *TLSClient) Close() error {
	// http.Client doesn't need explicit closing
	// Transport connections will be closed by idle timeout
	return nil
}

// GetFingerprintInfo возвращает информацию о текущем fingerprint
func (c *TLSClient) GetFingerprintInfo() map[string]interface{} {
	return map[string]interface{}{
		"chrome_version":  c.config.ChromeVersion,
		"randomize_ext":   c.config.RandomizeExtensions,
		"tls_min_version": "TLS1.2",
		"tls_max_version": "TLS1.3",
		"ja3_protection":   true,
		"ja4_protection":   true,
	}
}
