package main

import "testing"

func TestLoadConfig(t *testing.T) {
	c := &Config{}
	if err := LoadConfig("./contrib/pogolo.example.toml", c); err != nil {
		t.Fatalf("failed to load config: %s", err)
	}
}

func TestWriteConfig(t *testing.T) {
	if err := WriteDefaultConfig("/var/tmp/pogolo.toml"); err != nil {
		t.Fatalf("failed to write config: %s", err)
	}
}
