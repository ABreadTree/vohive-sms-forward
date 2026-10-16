package notify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
)

// TargetFingerprints identify delivery destinations without persisting secrets.
func TargetFingerprints(cfg Config) (bark, telegram string) {
	bark = targetHash("bark", cfg.BarkServer, cfg.BarkDeviceKey)
	telegram = targetHash("telegram", cfg.TelegramBotToken, strconv.FormatInt(cfg.TelegramChatID, 10))
	return bark, telegram
}
func targetHash(parts ...string) string {
	b, _ := json.Marshal(parts)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
