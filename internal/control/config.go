package control

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
)

const defaultListenAddress = "127.0.0.1:8080"

// Config contains non-secret process configuration for the control service.
type Config struct {
	ListenAddress string
	PublicOrigin  string
	CookieSecure  bool
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
	origin := os.Getenv("AKV_CONTROL_PUBLIC_ORIGIN")
	if origin == "" {
		origin = "http://" + address
	}
	parsedOrigin, err := url.Parse(origin)
	if err != nil || parsedOrigin.Scheme != "http" && parsedOrigin.Scheme != "https" || parsedOrigin.Host == "" || parsedOrigin.Path != "" || parsedOrigin.RawQuery != "" || parsedOrigin.Fragment != "" || parsedOrigin.User != nil {
		return Config{}, fmt.Errorf("AKV_CONTROL_PUBLIC_ORIGIN: invalid origin")
	}
	secure := parsedOrigin.Scheme == "https"
	if raw := os.Getenv("AKV_CONTROL_COOKIE_SECURE"); raw != "" {
		secure, err = strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("AKV_CONTROL_COOKIE_SECURE: %w", err)
		}
	}

	return Config{ListenAddress: address, PublicOrigin: origin, CookieSecure: secure}, nil
}
