package app

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/ABreadTree/vohive-sms-forward/internal/archive"
	"github.com/ABreadTree/vohive-sms-forward/internal/notify"
	"github.com/ABreadTree/vohive-sms-forward/internal/store"
	"github.com/ABreadTree/vohive-sms-forward/internal/telegram"
)

type NotificationClient interface {
	notify.Sender
	telegram.UpdateClient
}
type ClientFactory func(notify.Config) NotificationClient

type ChannelStatus struct {
	Configured bool   `json:"configured"`
	Active     bool   `json:"active"`
	Error      string `json:"error,omitempty"`
	Warning    string `json:"warning,omitempty"`
}
type NotificationStatus struct {
	Configured bool          `json:"configured"`
	Active     bool          `json:"active"`
	Error      string        `json:"error,omitempty"`
	Bark       ChannelStatus `json:"bark"`
	Telegram   ChannelStatus `json:"telegram"`
}

type NotificationManager struct {
	mu             sync.Mutex
	barkOpMu       sync.Mutex
	telegramOpMu   sync.Mutex
	path           string
	store          *store.Store
	archiver       *archive.Archiver
	sms            telegram.SMSSender
	sim            telegram.CurrentSIM
	sendGuard      func() bool
	factory        ClientFactory
	cfg            notify.Config
	status         NotificationStatus
	barkClient     NotificationClient
	telegramClient NotificationClient
	barkDispatcher *notify.Dispatcher
	tgDispatcher   *notify.Dispatcher
	poller         *telegram.Poller
}

func NewNotifications(path string, st *store.Store, a *archive.Archiver, sms telegram.SMSSender, sim telegram.CurrentSIM, sendGuard func() bool, factory ClientFactory) *NotificationManager {
	if factory == nil {
		factory = func(cfg notify.Config) NotificationClient { return notify.NewClient(cfg, nil) }
	}
	return &NotificationManager{path: path, store: st, archiver: a, sms: sms, sim: sim, sendGuard: sendGuard, factory: factory}
}

func (m *NotificationManager) StartFromDisk() error {
	m.barkOpMu.Lock()
	defer m.barkOpMu.Unlock()
	m.telegramOpMu.Lock()
	defer m.telegramOpMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.archiver.SetChannels(nil)
	m.stopLocked()
	m.status = NotificationStatus{}
	cfg, err := notify.LoadConfig(m.path)
	if errors.Is(err, os.ErrNotExist) {
		m.cfg = notify.Config{}
		return nil
	}
	if err != nil {
		m.status.Error = "config_invalid"
		return errors.New("notification config invalid")
	}
	m.cfg = cfg
	if !cfg.HasBark() && !cfg.HasTelegram() {
		m.status.Error = "config_invalid"
		return errors.New("notification config invalid")
	}
	var failed bool
	if cfg.HasBark() {
		m.status.Bark.Configured = true
		if cfg.ValidateBark() != nil {
			m.status.Bark.Error = "config_invalid"
			failed = true
		} else if err := m.prepareBarkLocked(); err != nil {
			m.status.Bark.Error = "prepare_failed"
			failed = true
		} else {
			m.startBarkLocked()
		}
	}
	if cfg.HasTelegram() {
		m.status.Telegram.Configured = true
		if cfg.ValidateTelegram() != nil {
			m.status.Telegram.Error = "config_invalid"
			failed = true
		} else if err := m.prepareTelegramLocked(); err != nil {
			m.status.Telegram.Error = "prepare_failed"
			failed = true
		} else {
			m.startTelegramLocked()
		}
	}
	m.refreshStatusLocked()
	m.setArchiveChannelsLocked()
	if failed {
		return errors.New("notification channel configuration failed")
	}
	return nil
}

func (m *NotificationManager) SaveBark(server, key string) error {
	m.barkOpMu.Lock()
	defer m.barkOpMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	next := m.cfg
	next.BarkServer, next.BarkDeviceKey = server, key
	if err := next.ValidateBark(); err != nil {
		return err
	}
	result, err := notify.SaveConfig(m.path, next)
	if err != nil {
		return errors.New("notification config save failed")
	}
	m.cfg = next
	m.status.Bark = ChannelStatus{Configured: true}
	m.setArchiveChannelsLocked()
	m.stopBarkLocked()
	if err := m.prepareBarkLocked(); err != nil {
		m.status.Bark.Error = "prepare_failed"
		m.refreshStatusLocked()
		return errors.New("notification target preparation failed")
	}
	m.startBarkLocked()
	if result.DurabilityWarning {
		m.status.Bark.Warning = "durability_uncertain"
	}
	m.refreshStatusLocked()
	m.setArchiveChannelsLocked()
	return nil
}

func (m *NotificationManager) SaveTelegram(token string, chatID int64) error {
	m.telegramOpMu.Lock()
	defer m.telegramOpMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	next := m.cfg
	next.TelegramBotToken, next.TelegramChatID = token, chatID
	if err := next.ValidateTelegram(); err != nil {
		return err
	}
	result, err := notify.SaveConfig(m.path, next)
	if err != nil {
		return errors.New("notification config save failed")
	}
	m.cfg = next
	m.status.Telegram = ChannelStatus{Configured: true}
	m.setArchiveChannelsLocked()
	m.stopTelegramLocked()
	if err := m.prepareTelegramLocked(); err != nil {
		m.status.Telegram.Error = "prepare_failed"
		m.refreshStatusLocked()
		return errors.New("notification target preparation failed")
	}
	m.startTelegramLocked()
	if result.DurabilityWarning {
		m.status.Telegram.Warning = "durability_uncertain"
	}
	m.refreshStatusLocked()
	m.setArchiveChannelsLocked()
	return nil
}

// SaveAndApply remains for existing in-process callers. The web API uses
// SaveBark and SaveTelegram so edits cannot replace the other channel.
func (m *NotificationManager) SaveAndApply(cfg notify.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := m.SaveTelegram(cfg.TelegramBotToken, cfg.TelegramChatID); err != nil {
		return err
	}
	return m.SaveBark(cfg.BarkServer, cfg.BarkDeviceKey)
}

func (m *NotificationManager) prepareBarkLocked() error {
	bark, _ := notify.TargetFingerprints(m.cfg)
	return m.store.PrepareNotificationTarget("bark", bark)
}
func (m *NotificationManager) prepareTelegramLocked() error {
	_, tg := notify.TargetFingerprints(m.cfg)
	return m.store.PrepareNotificationTarget("telegram", tg)
}
func (m *NotificationManager) startBarkLocked() {
	m.barkClient = m.factory(m.cfg)
	m.barkDispatcher = notify.NewDispatcher(m.store, m.barkClient, "bark")
	m.barkDispatcher.Start()
	m.status.Bark = ChannelStatus{Configured: true, Active: true}
}
func (m *NotificationManager) startTelegramLocked() {
	m.telegramClient = m.factory(m.cfg)
	m.tgDispatcher = notify.NewDispatcher(m.store, m.telegramClient, "telegram")
	processor := telegram.NewProcessor(m.store, m.sms, m.sim, m.cfg.TelegramChatID)
	if m.sendGuard != nil {
		processor.SetSendGuard(m.sendGuard)
	}
	m.poller = telegram.NewPoller(m.telegramClient, processor, m.store)
	m.tgDispatcher.Start()
	m.poller.Start(context.Background())
	m.status.Telegram = ChannelStatus{Configured: true, Active: true}
}
func (m *NotificationManager) stopBarkLocked() {
	if m.barkDispatcher != nil {
		m.barkDispatcher.Stop()
		m.barkDispatcher = nil
	}
	m.barkClient = nil
}
func (m *NotificationManager) stopTelegramLocked() {
	if m.poller != nil {
		m.poller.Stop()
		m.poller = nil
	}
	if m.tgDispatcher != nil {
		m.tgDispatcher.Stop()
		m.tgDispatcher = nil
	}
	m.telegramClient = nil
}
func (m *NotificationManager) stopLocked() {
	m.stopTelegramLocked()
	m.stopBarkLocked()
}
func (m *NotificationManager) setArchiveChannelsLocked() {
	var channels []string
	if m.status.Bark.Active {
		channels = append(channels, "bark")
	}
	if m.status.Telegram.Active {
		channels = append(channels, "telegram")
	}
	m.archiver.SetChannels(channels)
}
func (m *NotificationManager) refreshStatusLocked() {
	m.status.Configured = m.status.Bark.Configured || m.status.Telegram.Configured
	m.status.Active = m.status.Bark.Active || m.status.Telegram.Active
	m.status.Error = ""
	if m.status.Bark.Error != "" {
		m.status.Error = m.status.Bark.Error
	}
	if m.status.Telegram.Error != "" {
		m.status.Error = m.status.Telegram.Error
	}
}

func (m *NotificationManager) TestChannel(ctx context.Context, channel string) error {
	var opMu *sync.Mutex
	switch channel {
	case "bark":
		opMu = &m.barkOpMu
	case "telegram":
		opMu = &m.telegramOpMu
	default:
		return errors.New("invalid notification channel")
	}
	opMu.Lock()
	defer opMu.Unlock()
	m.mu.Lock()
	message := "VoHive Mac 测试通知 · " + channel + " · " + time.Now().Format(time.RFC3339)
	switch channel {
	case "bark":
		if !m.status.Bark.Active || m.barkClient == nil {
			m.mu.Unlock()
			return errors.New("notification unavailable")
		}
		client := m.barkClient
		m.mu.Unlock()
		return client.PushBark(ctx, "VoHive Mac 测试通知", message)
	case "telegram":
		if !m.status.Telegram.Active || m.telegramClient == nil {
			m.mu.Unlock()
			return errors.New("notification unavailable")
		}
		client := m.telegramClient
		m.mu.Unlock()
		return client.SendTelegram(ctx, message)
	}
	panic("unreachable")
}
func (m *NotificationManager) Status() NotificationStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}
func (m *NotificationManager) Stop() {
	m.barkOpMu.Lock()
	defer m.barkOpMu.Unlock()
	m.telegramOpMu.Lock()
	defer m.telegramOpMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.archiver.SetChannels(nil)
	m.stopLocked()
	m.status.Bark.Active = false
	m.status.Telegram.Active = false
	m.refreshStatusLocked()
}
