package config

import "testing"

func TestDefaultIsLoopbackOnly(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddress != "127.0.0.1:17890" {
		t.Fatalf("unexpected listen address: %s", cfg.ListenAddress)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default configuration is invalid: %v", err)
	}
}

func TestRejectsNonLoopbackAddress(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.ListenAddress = "0.0.0.0:17890"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected non-loopback listen address to be rejected")
	}
}
