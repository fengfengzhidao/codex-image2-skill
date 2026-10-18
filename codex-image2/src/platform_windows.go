package main

import (
	"encoding/base64"
	"errors"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"
)

type platformVault struct{}
type dataBlob struct {
	Size uint32
	Data *byte
}

var (
	crypt32       = syscall.NewLazyDLL("crypt32.dll")
	protectData   = crypt32.NewProc("CryptProtectData")
	unprotectData = crypt32.NewProc("CryptUnprotectData")
	kernel32      = syscall.NewLazyDLL("kernel32.dll")
	localFree     = kernel32.NewProc("LocalFree")
	moveFileEx    = kernel32.NewProc("MoveFileExW")
)

func dpapi(data []byte, decrypt bool) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty credential")
	}
	in := dataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out dataBlob
	proc := protectData
	if decrypt {
		proc = unprotectData
	}
	// CRYPTPROTECT_UI_FORBIDDEN; user-scoped encryption (no LOCAL_MACHINE flag).
	ok, _, _ := proc.Call(uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 1, uintptr(unsafe.Pointer(&out)))
	runtime.KeepAlive(data)
	if ok == 0 {
		return nil, errors.New("Windows 系统加密失败，请使用当前 Windows 用户重新配置密钥")
	}
	defer localFree.Call(uintptr(unsafe.Pointer(out.Data)))
	if out.Size == 0 || out.Size > 64*1024 {
		return nil, errors.New("invalid credential data")
	}
	view := unsafe.Slice(out.Data, int(out.Size))
	result := append([]byte(nil), view...)
	clear(view)
	return result, nil
}

func (platformVault) Seal(key string) (storedSecret, error) {
	data, err := dpapi([]byte(key), false)
	return storedSecret{Kind: "windows-dpapi", Data: base64.StdEncoding.EncodeToString(data)}, err
}
func (platformVault) Open(secret storedSecret) (string, error) {
	if secret.Kind != "windows-dpapi" {
		return "", errors.New("unsupported credential storage")
	}
	data, err := base64.StdEncoding.DecodeString(secret.Data)
	if err != nil {
		return "", errors.New("invalid encrypted credential")
	}
	plain, err := dpapi(data, true)
	return string(plain), err
}
func (platformVault) Delete(storedSecret) {}

func replaceConfig(from, to string) error {
	fromPtr, err := syscall.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toPtr, err := syscall.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	ok, _, callErr := moveFileEx.Call(uintptr(unsafe.Pointer(fromPtr)), uintptr(unsafe.Pointer(toPtr)), 1|8)
	if ok == 0 {
		return callErr
	}
	return nil
}

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000200 | 0x00000008}
}
func openBrowser(address string) error {
	cmd := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", address)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
