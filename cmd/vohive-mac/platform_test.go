package main

import "testing"

type networkLifecycle struct {
	events *[]string
	name   string
}

func (s networkLifecycle) Start() { *s.events = append(*s.events, "start:"+s.name) }
func (s networkLifecycle) Stop()  { *s.events = append(*s.events, "stop:"+s.name) }

func TestSMSOnlyNeverStartsOrStopsNetworkWorkers(t *testing.T) {
	var events []string
	stop := startNetworkServices(true, networkLifecycle{&events, "traffic"}, networkLifecycle{&events, "metered"})
	stop()
	if len(events) != 0 {
		t.Fatalf("SMS-only touched network workers: %v", events)
	}
}

func TestLegacyNetworkWorkersKeepLifecycle(t *testing.T) {
	var events []string
	stop := startNetworkServices(false, networkLifecycle{&events, "traffic"}, networkLifecycle{&events, "metered"})
	stop()
	want := []string{"start:traffic", "start:metered", "stop:metered", "stop:traffic"}
	if len(events) != len(want) {
		t.Fatalf("events=%v", events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events=%v", events)
		}
	}
}

func TestWatchdogDoesNotCompeteWithOpenWrtNetifd(t *testing.T) {
	for _, tc := range []struct {
		os            string
		smsOnly, want bool
	}{
		{"linux", false, false}, {"linux", true, false},
		{"darwin", false, true}, {"darwin", true, false},
	} {
		if got := watchdogEnabled(tc.os, tc.smsOnly); got != tc.want {
			t.Errorf("%s smsOnly=%v: %v", tc.os, tc.smsOnly, got)
		}
	}
}
