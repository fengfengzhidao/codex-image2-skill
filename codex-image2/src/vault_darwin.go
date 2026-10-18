package main

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

type platformVault struct{}

const keychainService = "codex-image2"

// security's interactive parser accepts quoted arguments and backslash escapes.
// Feed the password over stdin: it never appears in the process arguments.
func keychainQuote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

func (platformVault) Seal(key string) (storedSecret, error) {
	ref := randomID()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/security", "-i", "-q")
	cmd.Stdin = strings.NewReader("add-generic-password -a " + ref + " -s " + keychainService + " -w " + keychainQuote(key) + "\n")
	if err := cmd.Run(); err != nil {
		return storedSecret{}, errors.New("无法保存到 macOS 钥匙串，请解锁登录钥匙串后重试")
	}
	secret := storedSecret{Kind: "macos-keychain", Data: ref}
	saved, err := (platformVault{}).Open(secret)
	if err != nil || saved != key {
		(platformVault{}).Delete(secret)
		return storedSecret{}, errors.New("无法完整保存到 macOS 钥匙串，请检查密钥长度并解锁钥匙串后重试")
	}
	return secret, nil
}
func (platformVault) Open(secret storedSecret) (string, error) {
	if secret.Kind != "macos-keychain" {
		return "", errors.New("unsupported credential storage")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-a", secret.Data, "-s", keychainService, "-w").Output()
	if err != nil {
		return "", errors.New("无法读取 macOS 钥匙串，请解锁后重试")
	}
	return strings.TrimSpace(string(data)), nil
}
func (platformVault) Delete(secret storedSecret) {
	if secret.Kind != "macos-keychain" || secret.Data == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "/usr/bin/security", "delete-generic-password", "-a", secret.Data, "-s", keychainService).Run()
}
