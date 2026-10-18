package php

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testDirs() Dirs {
	return Dirs{
		Conf:    "/home/u/.config/rig/php",
		Logs:    "/home/u/.local/state/rig/logs",
		Runtime: "/run/user/1000/rig",
		Code:    "/home/u/code",
	}
}

func TestNames(t *testing.T) {
	if got := Default.ID(); got != "85" {
		t.Errorf("ID = %q", got)
	}
	if got := Default.UnitName(); got != "rig-php85.service" {
		t.Errorf("UnitName = %q", got)
	}
	if got := Socket(testDirs(), Default); got != "/run/user/1000/rig/php85/fpm.sock" {
		t.Errorf("Socket = %q", got)
	}
}

func TestPoolConfig(t *testing.T) {
	conf, err := PoolConfig(testDirs(), Default)
	if err != nil {
		t.Fatal(err)
	}
	s := string(conf)
	for _, want := range []string{
		"listen = /run/user/1000/rig/php85/fpm.sock\n",
		"listen.mode = 0600\n",
		"pm = ondemand\n",
		"security.limit_extensions = .php\n",
		"error_log = /home/u/.local/state/rig/logs/php85-fpm.log\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("pool missing %q", want)
		}
	}
	// Never a TCP listener, never another user.
	for _, banned := range []string{"127.0.0.1:", "0.0.0.0", "\nuser", "\ngroup", "listen.owner"} {
		if strings.Contains(s, banned) {
			t.Errorf("pool contains %q", banned)
		}
	}
}

func TestUnit(t *testing.T) {
	unit, err := Unit(testDirs(), Default)
	if err != nil {
		t.Fatal(err)
	}
	s := string(unit)
	for _, want := range []string{
		"ExecStart=/usr/bin/php-fpm --fpm-config /home/u/.config/rig/php/php85.conf --nodaemonize\n",
		"RuntimeDirectory=rig/php85\n",
		"Type=simple\n",
		"ProtectHome=tmpfs\n",
		"BindPaths=/home/u/code /home/u/.local/state/rig/logs /run/user/1000/rig/php85\n",
		"BindReadOnlyPaths=/home/u/.config/rig/php\n",
		"NoNewPrivileges=yes\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("unit missing %q", want)
		}
	}
	if strings.Contains(s, "MemoryDenyWriteExecute") {
		t.Error("MemoryDenyWriteExecute breaks PHP's PCRE JIT")
	}
}

func TestRejectsBadInput(t *testing.T) {
	for _, v := range []Version{{"85", "/usr/bin/php-fpm", ""}, {"8.5;", "/usr/bin/php-fpm", ""}, {"8.5", "php-fpm", ""}} {
		if _, err := PoolConfig(testDirs(), v); err == nil {
			t.Errorf("PoolConfig accepted version %+v", v)
		}
	}
	d := testDirs()
	d.Code = "/home/u/my code"
	if _, err := Unit(d, Default); err == nil {
		t.Error("Unit accepted an unsafe code path")
	}
}

// TestPoolConfigFPMAccepts runs the real "php-fpm --test" on the
// generated config if php-fpm is installed. It only parses the file.
func TestPoolConfigFPMAccepts(t *testing.T) {
	if _, err := exec.LookPath(Default.Binary); err != nil {
		t.Skip("php-fpm not installed")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := Dirs{
		Conf:    filepath.Join(root, "conf"),
		Logs:    filepath.Join(root, "rig", "logs"),
		Runtime: filepath.Join(root, "rig"),
		Code:    filepath.Join(root, "code"),
	}
	for _, dir := range []string{d.Conf, d.Logs, filepath.Join(d.Runtime, "php85")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	conf, err := PoolConfig(d, Default)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfPath(d, Default), conf, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(Default.Binary, "--fpm-config", ConfPath(d, Default), "--test").CombinedOutput()
	if err != nil {
		t.Fatalf("php-fpm --test failed: %v\n%s", err, out)
	}
}
