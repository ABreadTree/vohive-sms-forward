package server

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

type notificationPageResult struct {
	Aggregate            string `json:"aggregate"`
	Bark                 string `json:"bark"`
	Telegram             string `json:"telegram"`
	TelegramTestDisabled bool   `json:"telegramTestDisabled"`
}

func runNotificationPageScenario(t *testing.T, scenario string) notificationPageResult {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	input, err := json.Marshal(map[string]string{"block": notificationBlock(t), "scenario": scenario})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "testdata/notification_runtime.js")
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("notification page script: %v: %s", err, out)
	}
	var got notificationPageResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("parse output: %v: %s", err, out)
	}
	return got
}

func TestNotificationPageShowsFileLevelConfigError(t *testing.T) {
	got := runNotificationPageScenario(t, "file_error")
	if !strings.Contains(got.Aggregate, "config_invalid") {
		t.Fatalf("file error hidden: %+v", got)
	}
}

func TestNotificationPageOutOfOrderSaveResponsesKeepBothChannels(t *testing.T) {
	got := runNotificationPageScenario(t, "out_of_order")
	if got.Telegram != "已启用" || got.TelegramTestDisabled || got.Bark != "已启用" {
		t.Fatalf("older Bark response reverted Telegram: %+v", got)
	}
}
