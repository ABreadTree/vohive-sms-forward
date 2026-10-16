package notify

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode"
)

// Config contains private credentials. It must never be logged or returned by an API.
type Config struct {
	TelegramBotToken string `json:"telegram_bot_token"`
	TelegramChatID   int64  `json:"telegram_chat_id"`
	BarkServer       string `json:"bark_server"`
	BarkDeviceKey    string `json:"bark_device_key"`
}

type SaveResult struct{ DurabilityWarning bool }

func (c Config) HasBark() bool     { return c.BarkServer != "" || c.BarkDeviceKey != "" }
func (c Config) HasTelegram() bool { return c.TelegramBotToken != "" || c.TelegramChatID != 0 }

func (c Config) ValidateBark() error {
	if strings.TrimSpace(c.BarkDeviceKey) == "" || strings.ContainsAny(c.BarkDeviceKey, "/?#") || strings.IndexFunc(c.BarkDeviceKey, unicode.IsSpace) >= 0 {
		return errors.New("notification Bark key is invalid")
	}
	u, err := url.Parse(c.BarkServer)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("notification config needs a bare HTTPS Bark server URL")
	}
	return nil
}

func (c Config) ValidateTelegram() error {
	if strings.TrimSpace(c.TelegramBotToken) == "" || c.TelegramChatID <= 0 {
		return errors.New("notification Telegram credential or chat ID is invalid")
	}
	return nil
}

func ConfigPath(dataDir, override string) string {
	if override != "" {
		return override
	}
	return filepath.Join(dataDir, "notify.json")
}

func expectedOwner(euid int, sudoUID, sudoGID string) (int, int, error) {
	if euid != 0 {
		return euid, -1, nil
	}
	if sudoUID == "" && sudoGID == "" {
		return 0, 0, nil
	}
	uid, err1 := strconv.Atoi(sudoUID)
	gid, err2 := strconv.Atoi(sudoGID)
	if err1 != nil || err2 != nil || uid < 0 || gid < 0 {
		return 0, 0, errors.New("notification config owner is invalid")
	}
	return uid, gid, nil
}

func owner() (int, int, error) {
	return expectedOwner(os.Geteuid(), os.Getenv("SUDO_UID"), os.Getenv("SUDO_GID"))
}

func LoadConfig(path string) (Config, error) {
	var cfg Config
	uid, _, err := owner()
	if err != nil {
		return cfg, err
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return cfg, fmt.Errorf("notification config absent: %w", os.ErrNotExist)
		}
		return cfg, errors.New("notification config cannot be opened")
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return cfg, errors.New("notification config cannot be inspected")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return cfg, errors.New("notification config must be a regular 0600 file")
	}
	if info.Size() > 4096 {
		return cfg, errors.New("notification config is too large")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(uid) {
		return cfg, errors.New("notification config owner is invalid")
	}
	dec := json.NewDecoder(io.LimitReader(f, 4097))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, errors.New("notification config is not valid JSON")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return Config{}, errors.New("notification config has trailing data")
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if err := c.ValidateBark(); err != nil {
		return err
	}
	return c.ValidateTelegram()
}

func SaveConfig(path string, cfg Config) (SaveResult, error) {
	return saveConfig(path, cfg, func(d *os.File) error { return d.Sync() })
}

func saveConfig(path string, cfg Config, syncDir func(*os.File) error) (SaveResult, error) {
	var result SaveResult
	uid, gid, err := owner()
	if err != nil {
		return result, err
	}
	dir := filepath.Dir(path)
	parent, err := os.Lstat(dir)
	if err != nil {
		return result, errors.New("notification config directory is unavailable")
	}
	if !parent.IsDir() || parent.Mode().Perm()&0o022 != 0 {
		return result, errors.New("notification config directory is not private")
	}
	st, ok := parent.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(uid) {
		return result, errors.New("notification config directory owner is invalid")
	}
	old, err := os.Lstat(path)
	if err == nil && !old.Mode().IsRegular() {
		return result, errors.New("notification config target is not a regular file")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return result, errors.New("notification config target cannot be inspected")
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return result, errors.New("notification config encoding failed")
	}
	if len(b)+1 > 4096 {
		return result, errors.New("notification config is too large")
	}
	d, err := os.Open(dir)
	if err != nil {
		return result, errors.New("notification config directory sync failed")
	}
	defer d.Close()
	f, err := os.CreateTemp(dir, ".notify-*.tmp")
	if err != nil {
		return result, errors.New("notification config temporary file failed")
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return result, errors.New("notification config permissions failed")
	}
	if os.Geteuid() == 0 {
		if err := f.Chown(uid, gid); err != nil {
			f.Close()
			return result, errors.New("notification config ownership failed")
		}
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return result, errors.New("notification config write failed")
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return result, errors.New("notification config sync failed")
	}
	if err := f.Close(); err != nil {
		return result, errors.New("notification config close failed")
	}
	// Recheck the final path just before replacement; never follow a symlink.
	old, err = os.Lstat(path)
	if err == nil && !old.Mode().IsRegular() {
		return result, errors.New("notification config target is not a regular file")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return result, errors.New("notification config target cannot be inspected")
	}
	if err := os.Rename(tmp, path); err != nil {
		return result, errors.New("notification config replace failed")
	}
	if err := syncDir(d); err != nil {
		result.DurabilityWarning = true
	}
	return result, nil
}
