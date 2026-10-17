package netif

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

func resolveIface() string {
	usbID := func(key string, fallback uint16) uint16 {
		if n, err := strconv.ParseUint(os.Getenv(key), 16, 16); err == nil {
			return uint16(n)
		}
		return fallback
	}
	return resolveSysfsIface("/sys", os.Getenv("VOHIVE_NET_IFACE"), usbID("BAIWANG_VID", 0x2ca3), usbID("BAIWANG_PID", 0x4006))
}

func readCounters(iface string) (uint64, uint64, bool) {
	return readSysfsCounters("/sys", iface)
}

func readLink(iface string) LinkState {
	intf, err := net.InterfaceByName(iface)
	if err != nil {
		return LinkState{}
	}
	addrs, _ := intf.Addrs()
	return readSysfsLink("/sys", iface, intf.Flags, addrs)
}

func SetMetered(string, bool) error {
	return fmt.Errorf("macOS low-data interface flags are not supported on Linux")
}

func Privileged() bool { return os.Geteuid() == 0 }
