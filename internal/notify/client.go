package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Update is the Telegram Bot API subset relevant to private text commands.
type Update struct {
	UpdateID int64 `json:"update_id"`
	Message  struct {
		Date int64 `json:"date"`
		Chat struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
		From struct {
			ID int64 `json:"id"`
		} `json:"from"`
		Text string `json:"text"`
	} `json:"message"`
}

// APIError deliberately excludes response bodies, request URLs and payloads:
// all three could contain a Bot token, Bark key or SMS text.
type APIError struct {
	Platform   string
	Status     int
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.Status == 0 {
		return e.Platform + " API rejected request"
	}
	return fmt.Sprintf("%s API returned HTTP %d", e.Platform, e.Status)
}

type Client struct {
	cfg  Config
	http *http.Client
}

func NewClient(cfg Config, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 40 * time.Second}
	}
	return &Client{cfg: cfg, http: httpClient}
}

func (c *Client) PushBark(ctx context.Context, title, body string) error {
	endpoint := strings.TrimRight(c.cfg.BarkServer, "/") + "/push"
	payload := map[string]any{"device_key": c.cfg.BarkDeviceKey,
		"title": title, "body": body, "group": "VoHive SMS"}
	var resp struct {
		Code int `json:"code"`
	}
	if err := c.post(ctx, "bark", endpoint, payload, &resp); err != nil {
		return err
	}
	if resp.Code != 200 {
		return &APIError{Platform: "bark"}
	}
	return nil
}

func (c *Client) SendTelegram(ctx context.Context, text string) error {
	if text == "" {
		return errors.New("telegram message is empty")
	}
	runes := []rune(text)
	for len(runes) > 0 {
		n := len(runes)
		if n > 4096 {
			n = 4096
		}
		part := string(runes[:n])
		if err := c.telegramPost(ctx, "sendMessage", map[string]any{
			"chat_id": c.cfg.TelegramChatID, "text": part,
		}, nil); err != nil {
			return err
		}
		runes = runes[n:]
	}
	return nil
}

func (c *Client) GetUpdates(ctx context.Context, offset int64) ([]Update, error) {
	var updates []Update
	err := c.telegramPost(ctx, "getUpdates", map[string]any{
		"offset": offset, "timeout": 25, "allowed_updates": []string{"message"},
	}, &updates)
	return updates, err
}

func (c *Client) telegramPost(ctx context.Context, method string, payload any, result any) error {
	endpoint := "https://api.telegram.org/bot" + c.cfg.TelegramBotToken + "/" + method
	var envelope struct {
		OK         bool            `json:"ok"`
		Result     json.RawMessage `json:"result"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := c.post(ctx, "telegram", endpoint, payload, &envelope); err != nil {
		return err
	}
	if !envelope.OK {
		return &APIError{Platform: "telegram", RetryAfter: time.Duration(envelope.Parameters.RetryAfter) * time.Second}
	}
	if result != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, result); err != nil {
			return errors.New("telegram API returned malformed result")
		}
	}
	return nil
}

func (c *Client) post(ctx context.Context, platform, endpoint string, payload any, result any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%s payload encoding failed", platform)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("%s request construction failed", platform)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s transport failed", platform)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		after := parseRetryAfter(resp.Header.Get("Retry-After"))
		if after == 0 && platform == "telegram" {
			var rate struct {
				Parameters struct {
					RetryAfter int `json:"retry_after"`
				} `json:"parameters"`
			}
			if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rate) == nil &&
				rate.Parameters.RetryAfter > 0 {
				after = time.Duration(rate.Parameters.RetryAfter) * time.Second
			}
		}
		return &APIError{Platform: platform, Status: resp.StatusCode,
			RetryAfter: after}
	}
	if result != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(result); err != nil {
			return fmt.Errorf("%s API returned malformed JSON", platform)
		}
	}
	return nil
}

func parseRetryAfter(s string) time.Duration {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(s); err == nil && t.After(time.Now()) {
		return time.Until(t)
	}
	return 0
}
