package heartbeat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func privateDir(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	if err := os.Chmod(p, 0700); err != nil {
		t.Fatal(err)
	}
	return p
}
func validConfig() Config {
	return Config{Enabled: true, DeviceID: "istoreos-1", Name: "我的路由器", Endpoint: "https://ecs.example/v1/heartbeat", Secret: strings.Repeat("ab", 32), IntervalSeconds: 600}
}
func TestPrivateConfigRoundTrip(t *testing.T) {
	p := filepath.Join(privateDir(t), "heartbeat.json")
	c := validConfig()
	if err := SaveConfig(p, c); err != nil {
		s, _ := os.Stat(filepath.Dir(p))
		t.Logf("dir mode=%o validation=%v", s.Mode().Perm(), c.Validate())
		t.Fatal(err)
	}
	got, err := LoadConfig(p)
	if err != nil || got != c {
		t.Fatal(got, err)
	}
	os.Chmod(p, 0644)
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("insecure mode accepted")
	}
	os.Chmod(p, 0600)
	link := p + ".link"
	os.Symlink(p, link)
	if _, err := LoadConfig(link); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := SaveConfig(link, c); err == nil {
		t.Fatal("symlink replacement accepted")
	}
}
func TestConfigRejectsUnsafeInputs(t *testing.T) {
	for _, change := range []func(*Config){func(c *Config) { c.Endpoint = "http://ecs.example/v1/heartbeat" }, func(c *Config) { c.Endpoint += "?token=private" }, func(c *Config) { c.Endpoint = "https://user:pw@ecs.example/v1/heartbeat" }, func(c *Config) { c.Secret = "short" }, func(c *Config) { c.DeviceID = "../private" }, func(c *Config) { c.IntervalSeconds = 59 }, func(c *Config) { c.IntervalSeconds = 86401 }, func(c *Config) { c.Name = "x\ny" }} {
		c := validConfig()
		change(&c)
		if c.Validate() == nil {
			t.Fatal("accepted invalid config")
		}
	}
}

func TestConfigRejectsUnknownTrailingAndOversizeJSON(t *testing.T) {
	p := filepath.Join(privateDir(t), "heartbeat.json")
	for _, data := range []string{`{"unknown":"private"}`, `{} {}`, strings.Repeat(" ", 4097)} {
		if err := os.WriteFile(p, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(p); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
}
