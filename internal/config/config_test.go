package config

import (
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	t.Setenv(EnvControlListen, "")
	t.Setenv(EnvRelayListen, "")
	t.Setenv(EnvPublicControlURL, "")
	t.Setenv(EnvPublicRelay, "")
	t.Setenv(EnvInsecure, "")
	cfg, err := LoadServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ControlListen != DefaultControlListen || cfg.RelayListen != DefaultRelayListen {
		t.Fatalf("%+v", cfg)
	}
	if cfg.PublicControlURL != "http://127.0.0.1:41080" || cfg.PublicRelay != "127.0.0.1:41000" {
		t.Fatalf("%+v", cfg)
	}
	if cfg.Insecure {
		t.Fatal("insecure")
	}
}

func TestEnvAndFlagPrecedence(t *testing.T) {
	t.Setenv(EnvControlListen, "127.0.0.1:41081")
	t.Setenv(EnvRelayListen, "127.0.0.1:41001")
	t.Setenv(EnvPublicControlURL, "http://127.0.0.1:41081")
	t.Setenv(EnvPublicRelay, "127.0.0.1:41001")
	t.Setenv(EnvInsecure, "true")
	cfg, err := LoadServer([]string{"--control-listen", "127.0.0.1:18080", "--insecure=false"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ControlListen != "127.0.0.1:18080" {
		t.Fatal(cfg.ControlListen)
	}
	if cfg.RelayListen != "127.0.0.1:41001" || cfg.PublicRelay != "127.0.0.1:41001" {
		t.Fatalf("%+v", cfg)
	}
	if cfg.Insecure {
		t.Fatal("flag should clear insecure")
	}
}

func TestRefuseTCP443(t *testing.T) {
	t.Setenv(EnvControlListen, "")
	t.Setenv(EnvRelayListen, "")
	t.Setenv(EnvPublicControlURL, "")
	t.Setenv(EnvPublicRelay, "")
	t.Setenv(EnvInsecure, "")
	_, err := LoadServer([]string{"--control-listen", "127.0.0.1:443"})
	if err == nil || !strings.Contains(err.Error(), "443") {
		t.Fatal(err)
	}
}

func TestURLRewrite(t *testing.T) {
	httpBase, err := HTTPBase("wss://127.0.0.1:41080")
	if err != nil || httpBase != "https://127.0.0.1:41080" {
		t.Fatal(httpBase, err)
	}
	wsBase, err := WSBase("http://127.0.0.1:41080")
	if err != nil || wsBase != "ws://127.0.0.1:41080" {
		t.Fatal(wsBase, err)
	}
}
