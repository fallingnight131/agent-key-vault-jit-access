package control

import "testing"

func TestConfigFromEnv(t *testing.T) {
	t.Run("uses loopback default", func(t *testing.T) {
		t.Setenv("AKV_CONTROL_LISTEN_ADDRESS", "")

		config, err := ConfigFromEnv()
		if err != nil {
			t.Fatalf("ConfigFromEnv() error = %v", err)
		}
		if config.ListenAddress != defaultListenAddress {
			t.Fatalf("ListenAddress = %q, want %q", config.ListenAddress, defaultListenAddress)
		}
		if config.PublicOrigin != "http://"+defaultListenAddress || config.CookieSecure {
			t.Fatalf("config = %+v", config)
		}
	})

	t.Run("rejects malformed address", func(t *testing.T) {
		t.Setenv("AKV_CONTROL_LISTEN_ADDRESS", "not-an-address")

		if _, err := ConfigFromEnv(); err == nil {
			t.Fatal("ConfigFromEnv() error = nil, want error")
		}
	})

	t.Run("validates web security configuration", func(t *testing.T) {
		t.Setenv("AKV_CONTROL_PUBLIC_ORIGIN", "https://akv.example.test/path")
		if _, err := ConfigFromEnv(); err == nil {
			t.Fatal("path-bearing public origin was accepted")
		}
		t.Setenv("AKV_CONTROL_PUBLIC_ORIGIN", "https://akv.example.test")
		t.Setenv("AKV_CONTROL_COOKIE_SECURE", "true")
		config, err := ConfigFromEnv()
		if err != nil || !config.CookieSecure {
			t.Fatalf("ConfigFromEnv() config=%+v error=%v", config, err)
		}
	})
}
