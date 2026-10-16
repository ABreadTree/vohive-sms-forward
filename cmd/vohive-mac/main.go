// vohive-mac — a minimal SIM/modem management panel for macOS, built for
// BAIWANG/Quectel-style 4G USB dongles driven over raw USB (no serial
// driver needed). Web UI: SMS sending/inbox + cellular data usage.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ABreadTree/vohive-sms-forward/internal/app"
	"github.com/ABreadTree/vohive-sms-forward/internal/archive"
	"github.com/ABreadTree/vohive-sms-forward/internal/metered"
	"github.com/ABreadTree/vohive-sms-forward/internal/modem"
	"github.com/ABreadTree/vohive-sms-forward/internal/netif"
	"github.com/ABreadTree/vohive-sms-forward/internal/notify"
	"github.com/ABreadTree/vohive-sms-forward/internal/recovery"
	"github.com/ABreadTree/vohive-sms-forward/internal/server"
	"github.com/ABreadTree/vohive-sms-forward/internal/sims"
	"github.com/ABreadTree/vohive-sms-forward/internal/store"
)

// runATCLI implements `vohive-mac at 'AT+CSQ' ...` — an ad-hoc AT command
// console over the same raw-USB channel the server uses. Only one process
// can claim the AT interface, so stop the server before using it.
func runATCLI(cmds []string) {
	if len(cmds) == 0 {
		fmt.Fprintln(os.Stderr, "usage: vohive-mac at 'AT+CMD' ['AT+CMD2' ...]")
		os.Exit(2)
	}
	m := modem.New()
	for _, c := range cmds {
		resp, err := m.Cmd(c, 15*time.Second)
		fmt.Printf(">>> %s\n%s\n", c, strings.TrimSpace(resp))
		if err != nil && strings.TrimSpace(resp) == "" {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "at" {
		runATCLI(os.Args[2:])
		return
	}

	addr := flag.String("addr", "127.0.0.1:7676", "listen address")
	dataDir := flag.String("data", "data", "directory for the SQLite database")
	archiveDelete := flag.Bool("archive-delete", true,
		"delete SMS from modem/SIM storage after archiving to the database")
	smsOnly := flag.Bool("sms-only", false, "verify mobile data is off and lock the data-enable API")
	notifyConfigPath := flag.String("notify-config", "", "path to private 0600 Bark/Telegram JSON config")
	flag.Parse()
	if err := validateListenAddr(*addr, true); err != nil {
		log.Fatal(err)
	}

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatalf("data dir: %v", err)
	}

	st, err := store.Open(*dataDir)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	// Marking the ECM link low-data needs root, so this process is normally
	// started with sudo (see the metered package). SQLite's files would then
	// be created root-owned and a later non-sudo run could not write them, so
	// they are handed back to the user who invoked sudo.
	restoreDataOwnership(*dataDir)
	m := modem.New()
	guardState := modem.DataGuardPending
	if *smsOnly {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		state, err := modem.EnsureDataOff(ctx, m)
		cancel()
		guardState = state
		if err != nil {
			log.Printf("SMS-only data verification: %s (retry scheduled)", state)
		}
	}
	registry := sims.New(m, st)
	// resolve the card before anything writes a SIM-scoped row, so the first
	// bytes and messages of the process are attributed to it rather than to
	// the unknown SIM (if the dongle is absent, the poller picks it up later)
	registry.Refresh()
	registry.Start()
	traffic := netif.NewTracker(st, registry)
	traffic.Start()
	meter := metered.New(traffic, st, registry)
	meter.Start()
	archiver := archive.New(m, st, registry, *archiveDelete, nil)
	var watchdog *recovery.Watchdog
	if !*smsOnly {
		watchdog = recovery.New(m, traffic)
		watchdog.Start()
	}

	web := server.New(m, traffic, st, archiver, registry, meter, *smsOnly)
	if *smsOnly {
		web.SetDataGuardState(guardState)
	}
	guardCtx, stopGuard := context.WithCancel(context.Background())
	guardDone := make(chan struct{})
	var guardVerifyMu sync.Mutex
	verifyDataOff := func() bool {
		guardVerifyMu.Lock()
		defer guardVerifyMu.Unlock()
		web.SetDataGuardState(modem.DataGuardPending)
		checkCtx, cancel := context.WithTimeout(guardCtx, 45*time.Second)
		state, err := modem.EnsureDataOff(checkCtx, m)
		cancel()
		web.SetDataGuardState(state)
		if err != nil {
			log.Printf("SMS-only data verification: %s (retry scheduled)", state)
		}
		return state == modem.DataGuardOff
	}
	if *smsOnly {
		go func() {
			defer close(guardDone)
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-guardCtx.Done():
					return
				case <-ticker.C:
				}
				verifyDataOff()
			}
		}()
	} else {
		close(guardDone)
	}
	var sendGuard func() bool
	if *smsOnly {
		sendGuard = verifyDataOff
	}
	manager := app.NewNotifications(notificationConfigPath(*dataDir, *notifyConfigPath),
		st, archiver, m, registry, sendGuard, nil)
	web.SetNotificationSettings(manager)
	if err := startNotificationAndArchive(manager, archiver); err != nil {
		log.Printf("notification configuration unavailable; use local web settings to repair")
	}
	srv := &http.Server{Addr: *addr, Handler: web.Handler()}

	go func() {
		log.Printf("vohive-mac listening on http://%s", *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx) //nolint:errcheck
	manager.Stop()
	stopGuard()
	<-guardDone
	if watchdog != nil {
		watchdog.Stop()
	}
	meter.Stop()
	archiver.Stop()
	registry.Stop()
	traffic.Stop()
	st.Close() //nolint:errcheck
}

func notificationConfigPath(dataDir, override string) string {
	return notify.ConfigPath(dataDir, override)
}

// Startup must prepare destination fingerprints before the first modem drain.
// A malformed notification file never prevents the local SMS service from starting.
func startNotificationAndArchive(settings interface{ StartFromDisk() error }, archiver interface{ Start() }) error {
	err := settings.StartFromDisk()
	archiver.Start()
	return err
}

func validateListenAddr(addr string, _ bool) error {
	if addr != "127.0.0.1:7676" {
		return fmt.Errorf("web notification settings require 127.0.0.1:7676")
	}
	return nil
}

// restoreDataOwnership gives the database files back to the user who ran
// sudo. The app is meant to run as root (that is the only way to set the
// interface's low-data flags), but its data is the user's: without this, the
// first sudo run leaves vohive.db and its WAL owned by root and a subsequent
// plain `./vohive-mac` fails with "attempt to write a readonly database".
//
// Only the sudo case is handled: a genuine root login has no user to hand
// the files to, and a non-root run never created root-owned files.
func restoreDataOwnership(dataDir string) {
	if os.Geteuid() != 0 {
		return
	}
	uid, err1 := strconv.Atoi(os.Getenv("SUDO_UID"))
	gid, err2 := strconv.Atoi(os.Getenv("SUDO_GID"))
	if err1 != nil || err2 != nil || uid == 0 {
		return
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		path := filepath.Join(dataDir, e.Name())
		if err := os.Chown(path, uid, gid); err != nil {
			log.Printf("hand %s back to uid %d: %v", path, uid, err)
		}
	}
}
