package netif

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func fixtureFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}

func fixtureUSBNet(t *testing.T, root, name, device, vid, pid string) {
	t.Helper()
	usb := filepath.Join(root, "devices", device)
	fixtureFile(t, filepath.Join(usb, "idVendor"), vid+"\n")
	fixtureFile(t, filepath.Join(usb, "idProduct"), pid+"\n")
	intf := filepath.Join(usb, device+":1.4")
	if err := os.MkdirAll(intf, 0755); err != nil {
		t.Fatal(err)
	}
	iface := filepath.Join(root, "class", "net", name)
	if err := os.MkdirAll(iface, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(intf, filepath.Join(iface, "device")); err != nil {
		t.Fatal(err)
	}
}

func TestSysfsResolveMatchesUSBParentNotDefaultRoute(t *testing.T) {
	root := t.TempDir()
	fixtureUSBNet(t, root, "eth0", "1-1", "1234", "5678")
	fixtureUSBNet(t, root, "usb0", "1-2", "2ca3", "4006")
	if got := resolveSysfsIface(root, "", 0x2ca3, 0x4006); got != "usb0" {
		t.Fatalf("got %q", got)
	}
	fixtureUSBNet(t, root, "usb1", "1-3", "2ca3", "4006")
	if got := resolveSysfsIface(root, "", 0x2ca3, 0x4006); got != "" {
		t.Fatalf("ambiguous device selected: %q", got)
	}
	if got := resolveSysfsIface(root, "usb1", 0x2ca3, 0x4006); got != "usb1" {
		t.Fatalf("override got %q", got)
	}
	for _, invalid := range []string{"../net", "usb0/../../eth0", "/eth0", "missing"} {
		if got := resolveSysfsIface(root, invalid, 0x2ca3, 0x4006); got != "" {
			t.Errorf("invalid override %q selected %q", invalid, got)
		}
	}
}

func TestSysfsCountersAre64BitAndRejectPartialReads(t *testing.T) {
	root := t.TempDir()
	stats := filepath.Join(root, "class", "net", "wwan0", "statistics")
	fixtureFile(t, filepath.Join(stats, "rx_bytes"), "4294967301\n")
	fixtureFile(t, filepath.Join(stats, "tx_bytes"), "9000000000\n")
	if rx, tx, ok := readSysfsCounters(root, "wwan0"); !ok || rx != 4294967301 || tx != 9000000000 {
		t.Fatalf("%d %d %v", rx, tx, ok)
	}
	fixtureFile(t, filepath.Join(stats, "tx_bytes"), "broken")
	if _, _, ok := readSysfsCounters(root, "wwan0"); ok {
		t.Fatal("accepted broken counter")
	}
	if _, _, ok := readSysfsCounters(root, "../wwan0"); ok {
		t.Fatal("accepted path traversal")
	}
}

func TestLinuxLinkRequiresCarrierAndUsableIPv4(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "class", "net", "usb0", "carrier")
	fixtureFile(t, path, "1\n")
	addrs := []net.Addr{&net.IPNet{IP: net.ParseIP("169.254.1.1"), Mask: net.CIDRMask(16, 32)}}
	flags := net.FlagUp | net.FlagRunning
	state := readSysfsLink(root, "usb0", flags, addrs)
	if !state.Exists || !state.Up || state.RoutableV4 || state.Marked() {
		t.Fatalf("%+v", state)
	}
	addrs = append(addrs, &net.IPNet{IP: net.ParseIP("192.0.2.2"), Mask: net.CIDRMask(24, 32)})
	if !readSysfsLink(root, "usb0", flags, addrs).RoutableV4 {
		t.Fatal("usable IPv4 missed")
	}
	fixtureFile(t, path, "0\n")
	if readSysfsLink(root, "usb0", flags, addrs).Up {
		t.Fatal("carrier down reported up")
	}
	fixtureFile(t, path, "1\n")
	if readSysfsLink(root, "usb0", 0, addrs).Up {
		t.Fatal("administratively down reported up")
	}
	if readSysfsLink(root, "missing", flags, addrs).Exists {
		t.Fatal("missing interface exists")
	}
}
