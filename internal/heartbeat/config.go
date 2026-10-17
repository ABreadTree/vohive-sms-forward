package heartbeat

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"unicode"
)

type Config struct {
	Enabled         bool   `json:"enabled"`
	DeviceID        string `json:"device_id"`
	Name            string `json:"name"`
	Endpoint        string `json:"endpoint"`
	Secret          string `json:"secret"`
	IntervalSeconds int    `json:"interval_seconds"`
}

var devicePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var ErrConfig = errors.New("heartbeat config invalid")
var ErrDurability = errors.New("heartbeat config durability unconfirmed")

func (c Config) Validate() error {
	if c.IntervalSeconds < 60 || c.IntervalSeconds > 86400 {
		return ErrConfig
	}
	if !c.Enabled && c.DeviceID == "" && c.Name == "" && c.Endpoint == "" && c.Secret == "" {
		return nil
	}
	if !devicePattern.MatchString(c.DeviceID) || len(c.Name) > 160 || strings.TrimSpace(c.Name) == "" || strings.IndexFunc(c.Name, unicode.IsControl) >= 0 {
		return ErrConfig
	}
	key, err := hex.DecodeString(c.Secret)
	if err != nil || len(key) != 32 {
		return ErrConfig
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/v1/heartbeat" || u.RawPath != "" || u.ForceQuery {
		return ErrConfig
	}
	return nil
}
func configOwner() uint32 { return uint32(os.Geteuid()) }
func LoadConfig(path string) (Config, error) {
	var c Config
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return c, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	s, err := f.Stat()
	if err != nil || !s.Mode().IsRegular() || s.Mode().Perm() != 0600 || s.Size() > 4096 {
		return c, ErrConfig
	}
	st, ok := s.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != configOwner() {
		return c, ErrConfig
	}
	dec := json.NewDecoder(io.LimitReader(f, 4097))
	dec.DisallowUnknownFields()
	if dec.Decode(&c) != nil {
		return Config{}, ErrConfig
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return Config{}, ErrConfig
	}
	return c, c.Validate()
}
func SaveConfig(path string, c Config) error {
	if c.Validate() != nil {
		return ErrConfig
	}
	dir := filepath.Dir(path)
	s, err := os.Lstat(dir)
	if err != nil || !s.IsDir() || s.Mode().Perm()&0077 != 0 {
		return ErrConfig
	}
	st, ok := s.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != configOwner() {
		return ErrConfig
	}
	checkTarget := func() bool {
		s, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return true
		}
		return err == nil && s.Mode().IsRegular()
	}
	if !checkTarget() {
		return ErrConfig
	}
	b, err := json.Marshal(c)
	if err != nil || len(b) > 4095 {
		return ErrConfig
	}
	f, err := os.CreateTemp(dir, ".heartbeat-*.tmp")
	if err != nil {
		return ErrConfig
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if f.Chmod(0600) != nil {
		return ErrConfig
	}
	if _, err = f.Write(append(b, '\n')); err != nil {
		return ErrConfig
	}
	if f.Sync() != nil || f.Close() != nil {
		return ErrConfig
	}
	if !checkTarget() || os.Rename(f.Name(), path) != nil {
		return ErrConfig
	}
	d, err := os.Open(dir)
	if err != nil {
		return ErrDurability
	}
	defer d.Close()
	if d.Sync() != nil {
		return ErrDurability
	}
	return nil
}
