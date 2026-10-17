// Package netif is everything this app knows about the dongle's ECM network
// interface: finding it, reading its link state and low-data flags, setting
// those flags, and sampling its byte counters.
//
// It is deliberately below the SIM and the modem. The metered reconciler and
// the ECM watchdog both need to know what the interface is doing, and both
// read it through one shared platform backend rather than collecting their
// own interface snapshots every few seconds.
package netif

import (
	"regexp"
	"strings"
)

// LinkState is one platform snapshot (ifconfig on macOS, sysfs on Linux).
type LinkState struct {
	Exists      bool // the interface is there at all
	Up          bool // and its link is actually up ("status: active")
	RoutableV4  bool // it holds an IPv4 address worth routing over
	Expensive   bool // IFEF_EXPENSIVE
	Constrained bool // IFXF_CONSTRAINED
}

// Marked reports the low-data marking as this app applies it: both flags, set
// and cleared together.
func (l LinkState) Marked() bool { return l.Expensive && l.Constrained }

// Flags print inside the comma-separated eflags/xflags lists, so they are
// matched between delimiters — CONSTRAINED must not match ULTRA_CONSTRAINED,
// a different flag that ifconfig prints the same way.
var (
	expensiveRe   = regexp.MustCompile(`[<,]EXPENSIVE[,>]`)
	constrainedRe = regexp.MustCompile(`[<,]CONSTRAINED[,>]`)
)

func parseLink(out string) LinkState {
	return LinkState{
		Exists:      true,
		Up:          strings.Contains(out, "status: active"),
		RoutableV4:  hasRoutableV4(out),
		Expensive:   expensiveRe.MatchString(out),
		Constrained: constrainedRe.MatchString(out),
	}
}

// hasRoutableV4 reports whether the interface holds an IPv4 address that is
// worth routing over. A 169.254/16 address is what macOS self-assigns when
// its DHCP client found no server, so it counts as no IPv4 at all — the same
// as an interface that has not been configured yet. The distinction matters
// because the ECM link can be "status: active" and carrying IPv6 (SLAAC needs
// no server to answer) while every IPv4 route is missing; see the watchdog's
// healDHCP.
//
// Matched on whole fields rather than by substring: "inet6" must not match,
// and an interface can hold a real lease and a stale link-local address at
// once, in which case the real one wins.
func hasRoutableV4(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "inet" {
			continue
		}
		if !strings.HasPrefix(f[1], "169.254.") {
			return true
		}
	}
	return false
}
