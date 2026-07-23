package http

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestTLSClientGETRealSite проверяет, что uTLS-клиент действительно работает
// с реальным HTTPS-сайтом (не падает с malformed HTTP response как до фикса #43).
// Использует httpbin.org — стабильный тестовый эндпоинт.
//
// Это интеграционный тест (требует сети). Если сеть недоступна — пропуск.
func TestTLSClientGETRealSite(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client, err := NewTLSClient(DefaultTLSClientConfig)
	if err != nil {
		t.Fatalf("NewTLSClient failed: %v", err)
	}
	defer client.Close()

	client.GetHttpClient().Timeout = 30 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "https://httpbin.org/get", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("uTLS client request failed (the #43 bug): %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}

	if len(body) == 0 {
		t.Fatal("empty body — client did not receive content")
	}

	// httpbin.org/get возвращает JSON с полем "url"
	bodyStr := string(body)
	if !contains(bodyStr, `"url"`) {
		t.Fatalf("unexpected response (no 'url' field): %s", bodyStr[:min(len(bodyStr), 200)])
	}

	t.Logf("✅ uTLS client successfully fetched %d bytes, status %d", len(body), resp.StatusCode)
	t.Logf("   Proto: %s", resp.Proto)
}

// TestTLSClientMultipleRequests проверяет, что несколько последовательных
// запросов работают (пул h2-соединений не ломается, h1 path переиспользуется).
func TestTLSClientMultipleRequests(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client, err := NewTLSClient(DefaultTLSClientConfig)
	if err != nil {
		t.Fatalf("NewTLSClient failed: %v", err)
	}
	defer client.Close()

	client.GetHttpClient().Timeout = 30 * time.Second

	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		req, err := http.NewRequestWithContext(ctx, "GET", "https://httpbin.org/uuid", nil)
		if err != nil {
			cancel()
			t.Fatalf("request %d: failed to create: %v", i, err)
		}

		resp, err := client.Do(req)
		if err != nil {
			cancel()
			t.Fatalf("request %d failed: %v", i, err)
		}
		resp.Body.Close()
		cancel()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, resp.StatusCode)
		}
	}

	t.Log("✅ 3 sequential requests succeeded (ALPN routing + conn pool stable)")
}

// TestSetProxyValid проверяет, что SetProxy принимает валидные proxy URLs.
func TestSetProxyValid(t *testing.T) {
	client, err := NewTLSClient(DefaultTLSClientConfig)
	if err != nil {
		t.Fatalf("NewTLSClient failed: %v", err)
	}
	defer client.Close()

	tests := []struct {
		name    string
		proxy   string
		wantErr bool
	}{
		{"http proxy", "http://proxy.example.com:8080", false},
		{"https proxy", "https://proxy.example.com:8080", false},
		{"socks5 proxy", "socks5://127.0.0.1:1080", false},
		{"socks5h proxy", "socks5h://127.0.0.1:1080", false},
		{"empty (clear)", "", false},
		{"invalid scheme", "ftp://proxy.example.com:8080", true},
		{"garbage", "not-a-url", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.SetProxy(tt.proxy)
			if (err != nil) != tt.wantErr {
				t.Errorf("SetProxy(%q) error = %v, wantErr %v", tt.proxy, err, tt.wantErr)
			}
		})
	}
}

// TestGetFingerprintInfo проверяет, что fingerprint info корректна.
func TestGetFingerprintInfo(t *testing.T) {
	client, err := NewTLSClient(DefaultTLSClientConfig)
	if err != nil {
		t.Fatalf("NewTLSClient failed: %v", err)
	}
	defer client.Close()

	info := client.GetFingerprintInfo()

	if info["chrome_version"] != "HelloChrome_120" {
		t.Errorf("chrome_version = %v, want HelloChrome_120", info["chrome_version"])
	}
	if info["ja3_protection"] != true {
		t.Error("ja3_protection should be true")
	}
	if info["ja4_protection"] != true {
		t.Error("ja4_protection should be true")
	}
}

// contains — упрощённый strings.Contains (чтобы не тянуть import ради теста).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
