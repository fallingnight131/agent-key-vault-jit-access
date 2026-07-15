package control

import (
	"fmt"
	"net"
	"os"
)

const defaultListenAddress = "127.0.0.1:8080"

// Config contains non-secret process configuration for the control service.
type Config struct {
	ListenAddress string
}

// ConfigFromEnv loads process configuration. Secrets must be supplied through
// dedicated credential providers rather than added to this structure.
func ConfigFromEnv() (Config, error) {
	address := os.Getenv("AKV_CONTROL_LISTEN_ADDRESS")
	if address == "" {
		address = defaultListenAddress
	}

	if _, err := net.ResolveTCPAddr("tcp", address); err != nil {
		return Config{}, fmt.Errorf("AKV_CONTROL_LISTEN_ADDRESS: %w", err)
	}

	return Config{ListenAddress: address}, nil
}
