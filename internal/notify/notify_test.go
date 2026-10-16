package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func validConfig(server string) Config {
	return Config{TelegramBotToken: "secret-token-123", TelegramChatID: 12345,
		BarkServer: server, BarkDeviceKey: "secret-bark-key"}
}

func TestLoadConfigRejectsLoosePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify.json")
	data := `{"telegram_bot_token":"secret-token-123","telegram_chat_id":12345,"bark_server":"https://api.day.app","bark_device_key":"secret-bark-key"}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("group-readable secret file must be rejected")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TelegramChatID != 12345 || cfg.BarkServer != "https://api.day.app" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestBarkPostJSON(t *testing.T) {
	var got map[string]any
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/push" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type %q", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		fmt.Fprint(w, `{"code":200,"message":"success"}`)
	}))
	defer srv.Close()
	client := NewClient(validConfig(srv.URL), srv.Client())
	if err := client.PushBark(context.Background(), "新短信", "内容甲"); err != nil {
		t.Fatal(err)
	}
	if got["device_key"] != "secret-bark-key" || got["title"] != "新短信" || got["body"] != "内容甲" {
		t.Fatalf("unexpected Bark request: %+v", got)
	}
}

type rewriteTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c := r.Clone(r.Context())
	c.URL.Scheme, c.URL.Host, c.Host = rt.target.Scheme, rt.target.Host, rt.target.Host
	return rt.base.RoundTrip(c)
}

func telegramClient(t *testing.T, handler http.HandlerFunc) (*Client, func()) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	hc := srv.Client()
	hc.Timeout = 5 * time.Second
	hc.Transport = rewriteTransport{target: u, base: hc.Transport}
	return NewClient(validConfig("https://api.day.app"), hc), srv.Close
}

func TestTelegramRejectsAPIErrorWithoutTokenLeak(t *testing.T) {
	client, closeServer := telegramClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":false,"description":"secret-token-123 and private body echoed"}`)
	})
	defer closeServer()
	err := client.SendTelegram(context.Background(), "private body")
	if err == nil {
		t.Fatal("Telegram API refusal must be an error")
	}
	if strings.Contains(err.Error(), "secret-token-123") || strings.Contains(err.Error(), "private body") {
		t.Fatalf("sensitive data leaked in error: %v", err)
	}
}

func TestTelegramRetryAfterFromAPIResponse(t *testing.T) {
	client, closeServer := telegramClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"ok":false,"parameters":{"retry_after":7}}`)
	})
	defer closeServer()
	err := client.SendTelegram(context.Background(), "内容")
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Status != 429 || apiErr.RetryAfter != 7*time.Second {
		t.Fatalf("429 should carry retry_after=7s, got %#v", err)
	}
}

func TestTelegramSplitsLongText(t *testing.T) {
	var mu sync.Mutex
	var chunks []string
	client, closeServer := telegramClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		mu.Lock()
		chunks = append(chunks, body.Text)
		mu.Unlock()
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":1}}`)
	})
	defer closeServer()
	msg := strings.Repeat("界", 5000)
	if err := client.SendTelegram(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if len([]rune(chunks[0])) > 4096 || len([]rune(chunks[1])) > 4096 || strings.Join(chunks, "") != msg {
		t.Fatal("Telegram chunks must preserve full Unicode text under 4096 characters each")
	}
}

func TestGetUpdatesParsesPrivateChat(t *testing.T) {
	var gotOffset int64
	client, closeServer := telegramClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/getUpdates") {
			t.Errorf("path %s", r.URL.Path)
		}
		var req struct {
			Offset int64 `json:"offset"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		gotOffset = req.Offset
		fmt.Fprint(w, `{"ok":true,"result":[{"update_id":77,"message":{"chat":{"id":12345,"type":"private"},"from":{"id":12345},"text":"/help"}}]}`)
	})
	defer closeServer()
	updates, err := client.GetUpdates(context.Background(), 76)
	if err != nil {
		t.Fatal(err)
	}
	if gotOffset != 76 || len(updates) != 1 || updates[0].UpdateID != 77 || updates[0].Message.Chat.Type != "private" || updates[0].Message.From.ID != 12345 {
		t.Fatalf("offset=%d updates=%+v", gotOffset, updates)
	}
}
