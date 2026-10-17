package server

import (
	"context"
	"github.com/ABreadTree/vohive-sms-forward/internal/heartbeat"
	"net/http"
)

type HeartbeatSettings interface {
	Status() heartbeat.Status
	Save(heartbeat.Config) error
	CheckNow(context.Context) (heartbeat.Report, error)
	SendNow(context.Context) error
}

func (s *Server) SetHeartbeatSettings(h HeartbeatSettings) { s.heartbeatSettings = h }
func (s *Server) heartbeatStatus() heartbeat.Status {
	if s.heartbeatSettings == nil {
		return heartbeat.Status{ConfigError: "unavailable"}
	}
	status := s.heartbeatSettings.Status()
	status.Config.Secret = ""
	switch status.Error {
	case "", "self_check_failed", "delivery_failed":
	default:
		status.Error = "unavailable"
	}
	switch status.ConfigError {
	case "", "config_invalid", "durability_unconfirmed":
	default:
		status.ConfigError = "unavailable"
	}
	return status
}
func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/api/heartbeat" {
		if r.Method != http.MethodGet {
			notificationError(w, 405, "method_not_allowed")
			return
		}
		writeJSON(w, 200, s.heartbeatStatus())
		return
	}
	if r.Method != http.MethodPost {
		notificationError(w, 405, "method_not_allowed")
		return
	}
	if !notificationPostAllowed(r) {
		notificationError(w, 403, "forbidden_origin")
		return
	}
	if s.heartbeatSettings == nil {
		notificationError(w, 503, "unavailable")
		return
	}
	switch r.URL.Path {
	case "/api/heartbeat/config":
		var cfg heartbeat.Config
		if !decodeNotificationJSON(w, r, &cfg) {
			return
		}
		if err := s.heartbeatSettings.Save(cfg); err != nil {
			notificationError(w, 400, "invalid_configuration")
			return
		}
		writeJSON(w, 200, s.heartbeatStatus())
	case "/api/heartbeat/check", "/api/heartbeat/send":
		var empty struct{}
		if !decodeNotificationJSON(w, r, &empty) {
			return
		}
		if r.URL.Path == "/api/heartbeat/check" {
			if _, err := s.heartbeatSettings.CheckNow(r.Context()); err != nil {
				notificationError(w, 503, "check_unavailable")
				return
			}
		} else if err := s.heartbeatSettings.SendNow(r.Context()); err != nil {
			notificationError(w, 503, "heartbeat_not_sent")
			return
		}
		writeJSON(w, 200, s.heartbeatStatus())
	default:
		notificationError(w, 404, "not_found")
	}
}
