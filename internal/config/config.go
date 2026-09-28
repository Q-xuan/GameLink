// Package config loads server settings from flags and environment variables.
// Flags win over the environment, and the environment wins over the defaults.
package config

import (
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

const (
	DefaultControlListen    = "127.0.0.1:41080"
	DefaultRelayListen      = "0.0.0.0:41000"
	DefaultPublicControlURL = "wss://gamelink.aruyx.com"
	DefaultPublicRelay      = "195.72.187.81:41000"

	EnvControlListen    = "GAMELINK_CONTROL_LISTEN"
	EnvRelayListen      = "GAMELINK_RELAY_LISTEN"
	EnvPublicControlURL = "GAMELINK_PUBLIC_CONTROL_URL"
	EnvPublicRelay      = "GAMELINK_PUBLIC_RELAY"
	EnvInsecure         = "GAMELINK_INSECURE"
)

// Server is the control-plane and relay process configuration.
type Server struct {
	ControlListen    string
	RelayListen      string
	PublicControlURL string
	PublicRelay      string
	Insecure         bool
}

// LoadServer parses args after applying environment overrides to the defaults.
func LoadServer(args []string) (Server, error) {
	cfg := fromEnv()
	fs := flag.NewFlagSet("gamelink-server", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.ControlListen, "control-listen", cfg.ControlListen, "控制面 HTTP 监听地址")
	fs.StringVar(&cfg.RelayListen, "relay-listen", cfg.RelayListen, "UDP 中继监听地址")
	fs.StringVar(&cfg.PublicControlURL, "public-control-url", cfg.PublicControlURL, "对外公布的控制面 URL")
	fs.StringVar(&cfg.PublicRelay, "public-relay", cfg.PublicRelay, "对外公布的 UDP 中继地址")
	fs.BoolVar(&cfg.Insecure, "insecure", cfg.Insecure, "关闭 MAC 校验，仅用于本机测试")
	if err := fs.Parse(args); err != nil {
		return Server{}, err
	}
	if len(fs.Args()) != 0 {
		return Server{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if err := cfg.Validate(); err != nil {
		return Server{}, err
	}
	return cfg, nil
}

func fromEnv() Server {
	cfg := Server{
		ControlListen:    DefaultControlListen,
		RelayListen:      DefaultRelayListen,
		PublicControlURL: DefaultPublicControlURL,
		PublicRelay:      DefaultPublicRelay,
	}
	if v := os.Getenv(EnvControlListen); v != "" {
		cfg.ControlListen = v
	}
	if v := os.Getenv(EnvRelayListen); v != "" {
		cfg.RelayListen = v
	}
	if v := os.Getenv(EnvPublicControlURL); v != "" {
		cfg.PublicControlURL = v
	}
	if v := os.Getenv(EnvPublicRelay); v != "" {
		cfg.PublicRelay = v
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvInsecure))) {
	case "1", "true", "yes":
		cfg.Insecure = true
	}
	return cfg
}

// Validate rejects a TCP control listener on port 443. The process does not terminate TLS.
func (s Server) Validate() error {
	if _, port, err := split(s.ControlListen); err != nil {
		return fmt.Errorf("control listen: %w", err)
	} else if port == "443" {
		return fmt.Errorf("refusing to bind TCP 443")
	}
	if _, _, err := split(s.RelayListen); err != nil {
		return fmt.Errorf("relay listen: %w", err)
	}
	if _, _, err := split(s.PublicRelay); err != nil {
		return fmt.Errorf("public relay: %w", err)
	}
	u, err := url.Parse(s.PublicControlURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("public control URL is invalid")
	}
	switch u.Scheme {
	case "http", "https", "ws", "wss":
	default:
		return fmt.Errorf("public control URL scheme must be http, https, ws, or wss")
	}
	return nil
}

// DefaultControlURL returns the public control URL, or the environment override.
func DefaultControlURL() string {
	if v := os.Getenv(EnvPublicControlURL); v != "" {
		return v
	}
	return DefaultPublicControlURL
}

// DefaultRelay returns the public relay address, or the environment override.
func DefaultRelay() string {
	if v := os.Getenv(EnvPublicRelay); v != "" {
		return v
	}
	return DefaultPublicRelay
}

// HTTPBase converts a control URL to an HTTP origin for the REST API.
func HTTPBase(raw string) (string, error) {
	return rewriteScheme(raw, map[string]string{
		"wss":   "https",
		"ws":    "http",
		"https": "https",
		"http":  "http",
	})
}

// WSBase converts a control URL to a WebSocket origin.
func WSBase(raw string) (string, error) {
	return rewriteScheme(raw, map[string]string{
		"wss":   "wss",
		"ws":    "ws",
		"https": "wss",
		"http":  "ws",
	})
}

func rewriteScheme(raw string, schemes map[string]string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid control URL")
	}
	next, ok := schemes[u.Scheme]
	if !ok {
		return "", fmt.Errorf("unsupported control scheme %q", u.Scheme)
	}
	u.Scheme = next
	u.RawQuery = ""
	u.Fragment = ""
	out := u.String()
	return strings.TrimRight(out, "/"), nil
}

func split(addr string) (host, port string, err error) {
	host, port, err = net.SplitHostPort(addr)
	if err != nil {
		return "", "", err
	}
	if port == "" {
		return "", "", fmt.Errorf("missing port in %q", addr)
	}
	if _, err := net.LookupPort("tcp", port); err != nil {
		return "", "", err
	}
	return host, port, nil
}
