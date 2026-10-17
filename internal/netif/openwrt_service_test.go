package netif

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Execute the actual init script with a small rc.common/procd boundary
// fixture; do not create services or modify the host's /etc.
func TestOpenWrtServiceArgumentsAndDisabledState(t *testing.T) {
	script, err := filepath.Abs("../../openwrt/vohive-sms-forward/files/vohive-sms-forward.init")
	if err != nil {
		t.Fatal(err)
	}
	harness := `
config_load() { :; }
config_get() {
  case "$3" in
    data_dir) export "$1=$TEST_DATA";;
    vid) export "$1=${TEST_VID:-2ca3}";;
    pid) export "$1=4006";;
    at_interface) export "$1=3";;
    net_interface) export "$1=usb0";;
    *) export "$1=$4";;
  esac
}
config_get_bool() {
  case "$3" in
    enabled) export "$1=$TEST_ENABLED";;
    sms_only) export "$1=0";;
    *) export "$1=$4";;
  esac
}
procd_open_instance() { echo OPEN; }
procd_close_instance() { echo CLOSE; }
procd_set_param() { printf '<%s>' "$@"; printf '\n'; }
. "$1"
start_service
`
	for _, tc := range []struct {
		name, enabled, vid string
		wantStart          bool
	}{
		{"disabled", "0", "2ca3", false},
		{"enabled", "1", "2ca3", true},
		{"invalid USB ID", "1", "oops", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := filepath.Join(t.TempDir(), "private data")
			cmd := exec.Command("/bin/sh", "-c", harness, "fixture", script)
			cmd.Env = append(os.Environ(), "TEST_ENABLED="+tc.enabled, "TEST_VID="+tc.vid, "TEST_DATA="+data)
			out, runErr := cmd.CombinedOutput()
			if tc.wantStart {
				if runErr != nil {
					t.Fatalf("%v: %s", runErr, out)
				}
				for _, want := range []string{
					"<command></usr/bin/vohive-sms-forward><-addr><127.0.0.1:7676><-data><" + data + "><-notify-config><" + data + "/notify.json><-archive-delete=false><-sms-only=true>",
					"<env><BAIWANG_VID=2ca3><BAIWANG_PID=4006><BAIWANG_IFACE=3>",
					"<respawn>", "CLOSE",
				} {
					if !strings.Contains(string(out), want) {
						t.Errorf("missing %q in %s", want, out)
					}
				}
				if strings.Contains(string(out), "VOHIVE_NET_IFACE") {
					t.Errorf("SMS-only service configures a network interface: %s", out)
				}
				info, err := os.Stat(data)
				if err != nil || info.Mode().Perm() != 0700 {
					t.Fatalf("private data directory: %v %v", info, err)
				}
			} else {
				if tc.enabled == "0" && runErr != nil {
					t.Fatalf("disabled service: %v: %s", runErr, out)
				}
				if tc.enabled == "1" && runErr == nil {
					t.Fatalf("invalid configuration accepted: %s", out)
				}
				if strings.Contains(string(out), "OPEN") {
					t.Fatalf("unexpected service start: %s", out)
				}
				if _, err := os.Stat(data); !os.IsNotExist(err) {
					t.Fatalf("disabled/invalid service wrote data: %v", err)
				}
			}
		})
	}
}
