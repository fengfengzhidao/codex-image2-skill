//go:build !windows && !darwin

package main

import "errors"

type platformVault struct{}

func (platformVault) Seal(string) (storedSecret, error) {
	return storedSecret{}, errors.New("本地密钥存储目前支持 Windows 和 macOS；其他系统请使用环境变量")
}
func (platformVault) Open(storedSecret) (string, error) {
	return "", errors.New("unsupported credential storage")
}
func (platformVault) Delete(storedSecret) {}
