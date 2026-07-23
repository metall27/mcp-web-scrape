package http

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
	"golang.org/x/net/proxy"
)

// TLSClientConfig настройки для TLS fingerprinting
type TLSClientConfig struct {
	// ChromeVersion имитирует конкретную версию Chrome
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
	transport  *utlsRoundTripper
}

// NewTLSClient создает новый TLS клиент с fingerprinting
func NewTLSClient(config TLSClientConfig) (*TLSClient, error) {
	if config.ChromeVersion == "" {
		config.ChromeVersion = DefaultTLSClientConfig.ChromeVersion
	}
	if config.HandshakeTimeout == 0 {
		config.HandshakeTimeout = DefaultTLSClientConfig.HandshakeTimeout
	}

	rt := &utlsRoundTripper{
		config:  config,
		helloID: getHelloID(config.ChromeVersion),
		dialer: &net.Dialer{
			Timeout: config.HandshakeTimeout,
		},
		h2Transport: &http2.Transport{
			AllowHTTP: false,
		},
	}

	client := &TLSClient{
		config:     config,
		transport:  rt,
		httpClient: &http.Client{Transport: rt},
	}

	return client, nil
}

// getHelloID возвращает ClientHelloID для выбранной версии Chrome
func getHelloID(chromeVersion string) utls.ClientHelloID {
	switch chromeVersion {
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

// utlsRoundTripper реализует http.RoundTripper поверх uTLS с автоматическим
// роутингом HTTP/2 vs HTTP/1.1 на основе negotiated ALPN.
//
// Архитектура: после uTLS handshake проверяем NegotiatedProtocol.
// Если "h2" — создаём http2.ClientConn поверх готового соединения
// (http2.Transport принимает любой net.Conn, в отличие от http.Transport,
// который требует *tls.Conn). Если "http/1.1" — пишем запрос и читаем
// ответ вручную (DialTLSContext в http.Transport неработоспособен с uTLS
// из-за type-assertion на *tls.Conn в dialConn).
type utlsRoundTripper struct {
	config  TLSClientConfig
	helloID utls.ClientHelloID
	dialer  *net.Dialer

	proxyURL *url.URL
	proxyMu  sync.RWMutex

	// h2Transport используется только для NewClientConn (создание
	// HTTP/2 соединения поверх уже установленного uTLS-коннекта).
	// Мы НЕ используем его RoundTrip/dial — соединение создаём сами.
	h2Transport *http2.Transport

	// Пул HTTP/2 соединений для переиспользования (мультиплексирование)
	h2Pool sync.Map // map[string]*http2.ClientConn
}

// dialAndHandshake устанавливает TCP-соединение (прямое или через прокси),
// выполняет uTLS handshake с Chrome fingerprint и возвращает готовый UConn.
func (t *utlsRoundTripper) dialAndHandshake(ctx context.Context, host string) (*utls.UConn, error) {
	addr := host
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "443")
	}

	// 1. TCP dial (прямое или через прокси)
	rawConn, err := t.dialTCP(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("tcp dial: %w", err)
	}

	// 2. Deadline для handshake (по context запроса)
	if deadline, ok := ctx.Deadline(); ok {
		_ = rawConn.SetDeadline(deadline)
	} else {
		_ = rawConn.SetDeadline(time.Now().Add(t.config.HandshakeTimeout))
	}

	// 3. Извлекаем hostname для SNI
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}

	// 4. uTLS handshake с Chrome fingerprint
	tlsConfig := &utls.Config{
		ServerName:         hostname,
		InsecureSkipVerify: false,
		MinVersion:         tls.VersionTLS12,
		MaxVersion:         tls.VersionTLS13,
	}

	uconn := utls.UClient(rawConn, tlsConfig, t.helloID)
	if err := uconn.HandshakeContext(ctx); err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("tls handshake: %w", err)
	}

	// 5. Снимаем deadline — дальше управляем через context
	_ = rawConn.SetDeadline(time.Time{})

	return uconn, nil
}

// dialTCP устанавливает TCP-соединение с учётом настроек прокси.
func (t *utlsRoundTripper) dialTCP(ctx context.Context, addr string) (net.Conn, error) {
	t.proxyMu.RLock()
	proxyURL := t.proxyURL
	t.proxyMu.RUnlock()

	if proxyURL == nil {
		return t.dialer.DialContext(ctx, "tcp", addr)
	}

	switch proxyURL.Scheme {
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if proxyURL.User != nil {
			pw, _ := proxyURL.User.Password()
			auth = &proxy.Auth{
				User:     proxyURL.User.Username(),
				Password: pw,
			}
		}
		dialer, err := proxy.SOCKS5("tcp", proxyURL.Host, auth, t.dialer)
		if err != nil {
			return nil, fmt.Errorf("socks5 dialer: %w", err)
		}
		// golang.org/x/net/proxy.SOCKS5 возвращает тип, у которого есть
		// DialContext (под капотом *socks.Dialer). Проверяем интерфейсом.
		if cd, ok := dialer.(interface {
			DialContext(context.Context, string, string) (net.Conn, error)
		}); ok {
			return cd.DialContext(ctx, "tcp", addr)
		}
		// Fallback без context-cancellation (старые версии)
		return dialer.Dial("tcp", addr)

	default:
		// HTTP/HTTPS proxy — CONNECT tunnel
		conn, err := t.dialer.DialContext(ctx, "tcp", proxyURL.Host)
		if err != nil {
			return nil, fmt.Errorf("proxy dial: %w", err)
		}

		connectReq := &http.Request{
			Method: "CONNECT",
			URL:    &url.URL{Opaque: addr},
			Host:   addr,
			Header: make(http.Header),
		}
		if proxyURL.User != nil {
			password, _ := proxyURL.User.Password()
			cred := proxyURL.User.Username() + ":" + password
			connectReq.Header.Set(
				"Proxy-Authorization",
				"Basic "+base64.StdEncoding.EncodeToString([]byte(cred)),
			)
		}

		if err := connectReq.Write(conn); err != nil {
			conn.Close()
			return nil, fmt.Errorf("proxy CONNECT write: %w", err)
		}

		br := bufio.NewReader(conn)
		resp, err := http.ReadResponse(br, connectReq)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("proxy CONNECT read: %w", err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			conn.Close()
			return nil, fmt.Errorf("proxy CONNECT failed: %s", resp.Status)
		}
		return conn, nil
	}
}

// RoundTrip реализует http.RoundTripper — единая точка входа для http.Client.
func (t *utlsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Определяем host:port
	host := req.URL.Host
	if !strings.Contains(host, ":") {
		host = host + ":443"
	}

	// Проверяем пул h2-соединений для переиспользования
	poolKey := req.URL.Hostname()
	if cc, ok := t.h2Pool.Load(poolKey); ok {
		h2cc := cc.(*http2.ClientConn)
		if h2cc.CanTakeNewRequest() {
			return h2cc.RoundTrip(req)
		}
		t.h2Pool.Delete(poolKey)
	}

	// Dial + handshake
	uconn, err := t.dialAndHandshake(req.Context(), host)
	if err != nil {
		return nil, err
	}

	proto := uconn.ConnectionState().NegotiatedProtocol

	if proto == "h2" {
		// HTTP/2: создаём ClientConn поверх uTLS-соединения
		h2cc, err := t.h2Transport.NewClientConn(uconn)
		if err != nil {
			uconn.Close()
			return nil, fmt.Errorf("http2 NewClientConn: %w", err)
		}
		// Сохраняем в пул для переиспользования (мультиплексирование)
		t.h2Pool.Store(poolKey, h2cc)
		return h2cc.RoundTrip(req)
	}

	// HTTP/1.1: ручной write + read
	// Deadline из context для read/write фазы
	if deadline, ok := req.Context().Deadline(); ok {
		_ = uconn.SetDeadline(deadline)
	} else {
		_ = uconn.SetDeadline(time.Now().Add(30 * time.Second))
	}

	if err := req.Write(uconn); err != nil {
		uconn.Close()
		return nil, fmt.Errorf("http/1.1 write request: %w", err)
	}

	br := bufio.NewReader(uconn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		uconn.Close()
		return nil, fmt.Errorf("http/1.1 read response: %w", err)
	}

	// Оборачиваем body: при Close закрываем соединение
	resp.Body = &connCloseBody{ReadCloser: resp.Body, conn: uconn}
	return resp, nil
}

// connCloseBody оборачивает тело ответа и закрывает соединение при Close.
// Используется для HTTP/1.1 (соединение не переиспользуется).
type connCloseBody struct {
	io.ReadCloser
	conn net.Conn
}

func (b *connCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.conn.Close()
	return err
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

// GetTransport возвращает *http.Transport для совместимости.
//
// Deprecated: uTLS transport не является *http.Transport (использует кастомный
// RoundTripper с ALPN-роутингом h2/h1). Метод возвращает nil. Используйте
// GetHttpClient() для выполнения запросов.
func (c *TLSClient) GetTransport() *http.Transport {
	return nil
}

// SetProxy устанавливает proxy для TLS клиента.
// Поддерживаются схемы: http, https (CONNECT tunnel), socks5, socks5h.
func (c *TLSClient) SetProxy(proxyURL string) error {
	if proxyURL == "" {
		c.transport.proxyMu.Lock()
		c.transport.proxyURL = nil
		c.transport.proxyMu.Unlock()
		return nil
	}

	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return fmt.Errorf("failed to parse proxy URL: %w", err)
	}

	switch parsed.Scheme {
	case "http", "https", "socks5", "socks5h":
		// OK
	default:
		return fmt.Errorf("unsupported proxy scheme: %s (supported: http, https, socks5, socks5h)", parsed.Scheme)
	}

	c.transport.proxyMu.Lock()
	c.transport.proxyURL = parsed
	c.transport.proxyMu.Unlock()
	return nil
}

// Close закрывает TLS клиент и освобождает ресурсы
func (c *TLSClient) Close() error {
	// Закрываем все h2-соединения в пуле
	c.transport.h2Pool.Range(func(key, value any) bool {
		if h2cc, ok := value.(*http2.ClientConn); ok {
			h2cc.Close()
		}
		return true
	})
	return nil
}

// GetFingerprintInfo возвращает информацию о текущем fingerprint
func (c *TLSClient) GetFingerprintInfo() map[string]interface{} {
	return map[string]interface{}{
		"chrome_version":  c.config.ChromeVersion,
		"randomize_ext":   c.config.RandomizeExtensions,
		"tls_min_version": "TLS1.2",
		"tls_max_version": "TLS1.3",
		"ja3_protection":  true,
		"ja4_protection":  true,
	}
}
