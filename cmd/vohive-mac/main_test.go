package main

import "testing"

func TestMainWithoutNotifyConfigKeepsLocalSMS(t *testing.T) {
	if got := notificationConfigPath("data", ""); got != "data/notify.json" {
		t.Fatalf("default config path=%q", got)
	}
}

func TestSMSOnlyRequiresLocalListenAddress(t *testing.T) {
	if err := validateListenAddr(":7676", true); err == nil {
		t.Fatal("public binding accepted")
	}
	if err := validateListenAddr("127.0.0.1:7676", true); err != nil {
		t.Fatal(err)
	}
}
