package netif

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

const ifconfigPath = "/sbin/ifconfig"

// readLink reads the interface's link state and low-data flags in one pass.
// The ECM function asserts link state itself; after sleep/wake it can stay
// down (ifconfig "status: inactive") even though the interface still exists
// and netstat still lists it, so counter readability must not be used as "up".
//
// -v is required, not cosmetic: the eflags/xflags lists the low-data flags
// live in are printed only in verbose mode, and without it a marked link reads
// back as unmarked forever. Verbose mode can also exit non-zero while printing
// a perfectly good interface (it reads a supplemental sysctl that is not
// always permitted), so the output decides whether the read worked — a missing
// interface is the case that prints nothing at all.
func readLink(iface string) LinkState {
	out, _ := exec.Command("ifconfig", "-v", iface).Output()
	if len(bytes.TrimSpace(out)) == 0 {
		return LinkState{} // no such interface (unplugged, or renamed)
	}
	return parseLink(string(out))
}

// SetMetered sets or clears both low-data flags on the interface. Root only.
func SetMetered(iface string, on bool) error {
	args := []string{iface, "expensive", "constrained"}
	if !on {
		args = []string{iface, "-expensive", "-constrained"}
	}
	out, err := exec.Command(ifconfigPath, args...).CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%s: %s", ifconfigPath, msg)
		}
		return fmt.Errorf("%s: %w", ifconfigPath, err)
	}
	return nil
}

// Privileged reports whether this process can set the flags at all.
func Privileged() bool { return os.Geteuid() == 0 }

var bsdNameRe = regexp.MustCompile(`"BSD Name"\s*=\s*"(en\d+)"`)

// resolveIface finds the BSD interface name of the dongle's ECM function.
func resolveIface() string {
	out, err := exec.Command("ioreg", "-r", "-n", "Baiwang", "-l").Output()
	if err != nil {
		return ""
	}
	if m := bsdNameRe.FindSubmatch(out); m != nil {
		return string(m[1])
	}
	return ""
}

// readCounters parses `netstat -ibn` for the interface's Link-level row.
// Columns: Name Mtu Network Address Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll
func readCounters(iface string) (rx, tx uint64, ok bool) {
	out, err := exec.Command("netstat", "-ibn").Output()
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 10 || f[0] != iface || !strings.HasPrefix(f[2], "<Link#") {
			continue
		}
		// Some rows omit the Address column; Ibytes/Obytes stay at fixed
		// offsets from the end: ... Ibytes Opkts Oerrs Obytes Coll
		n := len(f)
		rxv, err1 := strconv.ParseUint(f[n-5], 10, 64)
		txv, err2 := strconv.ParseUint(f[n-2], 10, 64)
		if err1 != nil || err2 != nil {
			return 0, 0, false
		}
		return rxv, txv, true
	}
	return 0, 0, false
}
