package notify

import (
	"strings"
	"testing"
)

func TestFingerprintsChangeOnlyTheirTarget(t *testing.T) {
	cfg := validConfig("https://api.day.app")
	b1, t1 := TargetFingerprints(cfg)
	if len(b1) != 64 || len(t1) != 64 || b1 == t1 || strings.Contains(b1, cfg.BarkDeviceKey) || strings.Contains(t1, cfg.TelegramBotToken) {
		t.Fatalf("unsafe fingerprints %q %q", b1, t1)
	}
	cfg.BarkDeviceKey = "new-key"
	b2, t2 := TargetFingerprints(cfg)
	if b2 == b1 || t2 != t1 {
		t.Fatalf("Bark rotation changed wrong target")
	}
	cfg.TelegramBotToken = "new-token"
	b3, t3 := TargetFingerprints(cfg)
	if b3 != b2 || t3 == t2 {
		t.Fatalf("Telegram rotation changed wrong target")
	}
}
