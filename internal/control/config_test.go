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
	})

	t.Run("rejects malformed address", func(t *testing.T) {
		t.Setenv("AKV_CONTROL_LISTEN_ADDRESS", "not-an-address")

		if _, err := ConfigFromEnv(); err == nil {
			t.Fatal("ConfigFromEnv() error = nil, want error")
		}
	})
}
