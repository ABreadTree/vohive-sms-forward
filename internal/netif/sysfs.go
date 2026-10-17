package netif

// Linux sysfs readers are kept independent of the host OS so they can be
// exercised using fixture trees without a modem or a Linux kernel.
import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func validIface(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00")
}

// Multiple matching interfaces are deliberately ambiguous: the user must
// select VOHIVE_NET_IFACE instead of accidentally counting the router's LAN.
func resolveSysfsIface(root, override string, vid, pid uint16) string {
	base := filepath.Join(root, "class", "net")
	if override != "" {
		if !validIface(override) {
			return ""
		}
		if info, err := os.Stat(filepath.Join(base, override)); err == nil && info.IsDir() {
			return override
		}
		return ""
	}
	entries, _ := os.ReadDir(base)
	match := ""
	for _, entry := range entries {
		device, err := filepath.EvalSymlinks(filepath.Join(base, entry.Name(), "device"))
		if err != nil {
			continue
		}
		for dir := device; ; dir = filepath.Dir(dir) {
			v, ve := readSysfsUint(filepath.Join(dir, "idVendor"), 16)
			p, pe := readSysfsUint(filepath.Join(dir, "idProduct"), 16)
			if ve == nil && pe == nil {
				if v == uint64(vid) && p == uint64(pid) {
					if match != "" {
						return ""
					}
					match = entry.Name()
				}
				break // nearest USB device, not an upstream hub
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	return match
}

func readSysfsUint(path string, base int) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(data)), base, 64)
}

func readSysfsCounters(root, iface string) (rx, tx uint64, ok bool) {
	if !validIface(iface) {
		return 0, 0, false
	}
	base := filepath.Join(root, "class", "net", iface, "statistics")
	rx, erx := readSysfsUint(filepath.Join(base, "rx_bytes"), 10)
	tx, etx := readSysfsUint(filepath.Join(base, "tx_bytes"), 10)
	if erx != nil || etx != nil {
		return 0, 0, false
	}
	return rx, tx, true
}

func readSysfsLink(root, iface string, flags net.Flags, addrs []net.Addr) LinkState {
	if !validIface(iface) {
		return LinkState{}
	}
	base := filepath.Join(root, "class", "net", iface)
	if _, err := os.Stat(base); err != nil {
		return LinkState{}
	}
	carrier, err := readSysfsUint(filepath.Join(base, "carrier"), 10)
	state := LinkState{Exists: true, Up: flags&net.FlagUp != 0 && flags&net.FlagRunning != 0 && err == nil && carrier == 1}
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err == nil && ip.To4() != nil && ip.IsGlobalUnicast() && !ip.IsLinkLocalUnicast() {
			state.RoutableV4 = true
		}
	}
	return state
}
