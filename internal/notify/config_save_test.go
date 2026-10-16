package notify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestConfigPathDefaultsToDataDir(t *testing.T) {
	dir := t.TempDir()
	if got := ConfigPath(dir, ""); got != filepath.Join(dir, "notify.json") {
		t.Fatalf("default path %q", got)
	}
	if got := ConfigPath(dir, "/private/config.json"); got != "/private/config.json" {
		t.Fatalf("override path %q", got)
	}
}

func TestSaveConfigAtomicAndPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notify.json")
	cfg := validConfig("https://api.day.app")
	if _, err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != cfg {
		t.Fatalf("config mismatch: %+v", got)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "notify.json" {
		t.Fatalf("leftovers: %v", entries)
	}
}

func TestSaveFailurePreservesExistingBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notify.json")
	before := []byte("old private bytes")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	// A world-writable parent is invalid independently of process privileges.
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveConfig(path, validConfig("https://api.day.app")); err == nil {
		t.Fatal("saved in unsafe parent")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(before) {
		t.Fatalf("old bytes replaced: %q", got)
	}
}

func TestSaveRejectsSymlinkAndWorldWritableParent(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	link := filepath.Join(dir, "notify.json")
	if err := os.WriteFile(real, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveConfig(link, validConfig("https://api.day.app")); err == nil {
		t.Fatal("symlink accepted")
	}
	got, err := os.ReadFile(real)
	if err != nil || string(got) != "untouched" {
		t.Fatalf("target changed %q %v", got, err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveConfig(link, validConfig("https://api.day.app")); err == nil {
		t.Fatal("world-writable parent accepted")
	}
}

func TestLoadConfigRejectsTrailingJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify.json")
	data := `{"telegram_bot_token":"x","telegram_chat_id":1,"bark_server":"https://api.day.app","bark_device_key":"y"} {}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("second JSON value accepted")
	}
}

func TestExpectedOwnerUnderSudo(t *testing.T) {
	uid, gid, err := expectedOwner(0, "501", "20")
	if err != nil || uid != 501 || gid != 20 {
		t.Fatalf("sudo owner %d:%d %v", uid, gid, err)
	}
	uid, gid, err = expectedOwner(502, "501", "20")
	if err != nil || uid != 502 || gid != -1 {
		t.Fatalf("ordinary owner %d:%d %v", uid, gid, err)
	}
	uid, gid, err = expectedOwner(0, "", "")
	if err != nil || uid != 0 || gid != 0 {
		t.Fatalf("root owner %d:%d %v", uid, gid, err)
	}
	if _, _, err = expectedOwner(0, "not-a-uid", "20"); err == nil || strings.Contains(err.Error(), "not-a-uid") {
		t.Fatalf("invalid sudo uid error %v", err)
	}
}

func TestLoadConfigRejectsOversizeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify.json")
	cfg := validConfig("https://api.day.app")
	base, _ := json.Marshal(cfg)
	cfg.TelegramBotToken = strings.Repeat("x", 4097-len(base)+len(cfg.TelegramBotToken))
	b, _ := json.Marshal(cfg)
	if len(b) != 4097 {
		t.Fatalf("fixture length %d", len(b))
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("oversize config accepted")
	}
}

func TestPartialConfigChannelValidation(t *testing.T) {
	bark := Config{BarkServer: "https://api.day.app", BarkDeviceKey: "pure-key"}
	if !bark.HasBark() || bark.HasTelegram() || bark.ValidateBark() != nil {
		t.Fatalf("Bark-only config rejected: %+v", bark)
	}
	tg := Config{TelegramBotToken: "token", TelegramChatID: 123}
	if !tg.HasTelegram() || tg.HasBark() || tg.ValidateTelegram() != nil {
		t.Fatalf("Telegram-only config rejected: %+v", tg)
	}
	bark.BarkDeviceKey = "key/extra"
	if bark.ValidateBark() == nil {
		t.Fatal("Bark path accepted as pure key")
	}
	bark.BarkDeviceKey = "key\u00a0extra"
	if bark.ValidateBark() == nil {
		t.Fatal("Bark key with Unicode whitespace accepted")
	}
}

func TestLoadConfigDoesNotBlockOnFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify.json")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := LoadConfig(path); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("FIFO blocked config load")
	}
}

func TestSaveConfig4096ByteBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify.json")
	cfg := Config{TelegramBotToken: "x", TelegramChatID: 123}
	base, _ := json.Marshal(cfg)
	cfg.TelegramBotToken = strings.Repeat("x", 4095-len(base)+1)
	if b, _ := json.Marshal(cfg); len(b) != 4095 {
		t.Fatalf("fixture length %d", len(b))
	}
	if _, err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err != nil {
		t.Fatalf("4096-byte file did not reload: %v", err)
	}
	cfg.TelegramBotToken += "x"
	if _, err := SaveConfig(path, cfg); err == nil {
		t.Fatal("4097-byte file accepted")
	}
}

func TestSaveConfigPostRenameSyncWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify.json")
	cfg := Config{TelegramBotToken: "token", TelegramChatID: 123}
	result, err := saveConfig(path, cfg, func(*os.File) error { return syscall.EIO })
	if err != nil || !result.DurabilityWarning {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	got, err := LoadConfig(path)
	if err != nil || got != cfg {
		t.Fatalf("committed config=%+v err=%v", got, err)
	}
}
