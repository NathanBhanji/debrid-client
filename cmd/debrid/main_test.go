package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.HasPrefix(out.String(), "debrid ") {
		t.Fatalf("unexpected output %q", out.String())
	}
}

func TestConfigInitAndShow(t *testing.T) {
	t.Setenv("DEBRID_CONFIG", "")
	dir := t.TempDir()
	cfgPath := dir + "/config.yaml"

	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetArgs([]string{"config", "init", "--config", cfgPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("config init: %v", err)
	}

	out.Reset()
	root = newRootCmd()
	root.SetOut(&out)
	root.SetArgs([]string{"config", "show", "--config", cfgPath, "--server-listen", "1.2.3.4:9"})
	if err := root.Execute(); err != nil {
		t.Fatalf("config show: %v", err)
	}
	if !strings.Contains(out.String(), "listen: 1.2.3.4:9") {
		t.Fatalf("flag not reflected in show output:\n%s", out.String())
	}
}
