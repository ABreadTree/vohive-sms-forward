package server

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"

	"github.com/ABreadTree/vohive-sms-forward/internal/app"
	"github.com/ABreadTree/vohive-sms-forward/internal/notify"
)

type NotificationSettings interface {
	Status() app.NotificationStatus
	SaveBark(string, string) error
	SaveTelegram(string, int64) error
	TestChannel(context.Context, string) error
}

func (s *Server) SetNotificationSettings(n NotificationSettings) { s.notificationSettings = n }

func safeNotificationStatus(status app.NotificationStatus) app.NotificationStatus {
	safeError := func(category string) string {
		switch category {
		case "", "config_invalid", "save_failed", "prepare_failed", "unavailable":
			return category
		default:
			return "unavailable"
		}
	}
	status.Error = safeError(status.Error)
	status.Bark.Error = safeError(status.Bark.Error)
	status.Telegram.Error = safeError(status.Telegram.Error)
	if status.Bark.Warning != "" && status.Bark.Warning != "durability_uncertain" {
		status.Bark.Warning = "unavailable"
	}
	if status.Telegram.Warning != "" && status.Telegram.Warning != "durability_uncertain" {
		status.Telegram.Warning = "unavailable"
	}
	return status
}
func (s *Server) notificationStatus() app.NotificationStatus {
	if s.notificationSettings == nil {
		return app.NotificationStatus{Error: "unavailable"}
	}
	return safeNotificationStatus(s.notificationSettings.Status())
}
func notificationError(w http.ResponseWriter, code int, category string) {
	writeJSON(w, code, map[string]string{"error": category})
}
func notificationPostAllowed(r *http.Request) bool {
	return r.Host == "127.0.0.1:7676" && r.Header.Get("Origin") == "http://127.0.0.1:7676"
}
func decodeNotificationJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		notificationError(w, 400, "invalid_json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		notificationError(w, 400, "invalid_json")
		return false
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		notificationError(w, 400, "invalid_json")
		return false
	}
	return true
}
func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, s.notificationStatus())
	default:
		notificationError(w, 405, "method_not_allowed")
	}
}

func (s *Server) handleNotificationBark(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		notificationError(w, 405, "method_not_allowed")
		return
	}
	if !notificationPostAllowed(r) {
		notificationError(w, 403, "forbidden_origin")
		return
	}
	var req struct {
		Server string `json:"bark_server"`
		Key    string `json:"bark_device_key"`
	}
	if !decodeNotificationJSON(w, r, &req) {
		return
	}
	if err := (notify.Config{BarkServer: req.Server, BarkDeviceKey: req.Key}).ValidateBark(); err != nil {
		notificationError(w, 400, "invalid_configuration")
		return
	}
	if s.notificationSettings == nil {
		notificationError(w, 503, "unavailable")
		return
	}
	if err := s.notificationSettings.SaveBark(req.Server, req.Key); err != nil {
		notificationError(w, 500, "save_failed")
		return
	}
	writeJSON(w, 200, s.notificationStatus())
}
func (s *Server) handleNotificationTelegram(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		notificationError(w, 405, "method_not_allowed")
		return
	}
	if !notificationPostAllowed(r) {
		notificationError(w, 403, "forbidden_origin")
		return
	}
	var req struct {
		Token  string `json:"telegram_bot_token"`
		ChatID int64  `json:"telegram_chat_id"`
	}
	if !decodeNotificationJSON(w, r, &req) {
		return
	}
	if err := (notify.Config{TelegramBotToken: req.Token, TelegramChatID: req.ChatID}).ValidateTelegram(); err != nil {
		notificationError(w, 400, "invalid_configuration")
		return
	}
	if s.notificationSettings == nil {
		notificationError(w, 503, "unavailable")
		return
	}
	if err := s.notificationSettings.SaveTelegram(req.Token, req.ChatID); err != nil {
		notificationError(w, 500, "save_failed")
		return
	}
	writeJSON(w, 200, s.notificationStatus())
}
func (s *Server) handleNotificationTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		notificationError(w, 405, "method_not_allowed")
		return
	}
	if !notificationPostAllowed(r) {
		notificationError(w, 403, "forbidden_origin")
		return
	}
	var req struct {
		Channel string `json:"channel"`
	}
	if !decodeNotificationJSON(w, r, &req) {
		return
	}
	if req.Channel != "bark" && req.Channel != "telegram" {
		notificationError(w, 400, "invalid_channel")
		return
	}
	if s.notificationSettings == nil {
		notificationError(w, 503, "unavailable")
		return
	}
	status := s.notificationStatus()
	if (req.Channel == "bark" && !status.Bark.Active) || (req.Channel == "telegram" && !status.Telegram.Active) {
		notificationError(w, 503, "unavailable")
		return
	}
	if err := s.notificationSettings.TestChannel(r.Context(), req.Channel); err != nil {
		notificationError(w, 502, "test_failed")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
