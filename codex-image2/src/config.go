package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var errNotConfigured = errors.New("尚未配置图片服务。请运行 configure，在本地配置页填写 API 地址和密钥，无需重启 Codex")

type storedSecret struct {
	Kind string `json:"kind"`
	Data string `json:"data"`
}

type keyVault interface {
	Seal(string) (storedSecret, error)
	Open(storedSecret) (string, error)
	Delete(storedSecret)
}

type diskConfig struct {
	Version int          `json:"version"`
	APIURL  string       `json:"api_url"`
	Secret  storedSecret `json:"secret"`
}

type settings struct {
	APIURL  string
	APIKey  string
	Source  string
	Storage string
}

type configStore struct {
	path  string
	vault keyVault
}

func defaultConfigStore() (*configStore, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, errors.New("无法找到用户配置目录，请检查系统的用户目录设置")
	}
	return &configStore{path: filepath.Join(dir, "codex-image2", "config.json"), vault: platformVault{}}, nil
}

func randomID() string {
	var data [24]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic("system random source unavailable")
	}
	return hex.EncodeToString(data[:])
}

// Keep address and key together: an existing local config never borrows an
// environment key, and a broken local config never silently changes providers.
func (s *configStore) Resolve(requireKey bool) (settings, error) {
	data, err := os.ReadFile(s.path)
	if err == nil {
		var disk diskConfig
		if len(data) > 64*1024 || json.Unmarshal(data, &disk) != nil || disk.Version != 1 || disk.Secret.Data == "" {
			return settings{Source: "local"}, errors.New("本地配置损坏，请运行 configure 重新保存配置")
		}
		base, err := normalizeAPIBase(disk.APIURL)
		if err != nil {
			return settings{Source: "local"}, errors.New("本地 API 地址无效，请运行 configure 重新配置")
		}
		value := settings{APIURL: base, Source: "local", Storage: disk.Secret.Kind}
		if requireKey {
			value.APIKey, err = s.vault.Open(disk.Secret)
			if err != nil || validateKey(value.APIKey) != nil {
				return settings{APIURL: base, Source: "local", Storage: disk.Secret.Kind}, errors.New("无法读取已保存的密钥，请解锁系统凭据存储，或运行 configure 重新填写密钥")
			}
		}
		return value, nil
	}
	if !os.IsNotExist(err) {
		return settings{}, errors.New("无法读取本地配置，请检查用户配置目录的访问权限")
	}
	key := strings.TrimSpace(os.Getenv("CODEX_API_KEY"))
	base := strings.TrimSpace(os.Getenv("CODEX_API_URL"))
	if base == "" {
		base = defaultAPIURL
	}
	base, err = normalizeAPIBase(base)
	if err != nil {
		return settings{Source: "environment"}, err
	}
	value := settings{APIURL: base, APIKey: key, Source: "environment", Storage: "environment"}
	if key == "" {
		value.Source = "none"
		value.Storage = ""
		if requireKey {
			return value, errNotConfigured
		}
	} else if err := validateKey(key); err != nil {
		return value, err
	}
	return value, nil
}

func resolveSettings(requireKey bool) (settings, error) {
	store, err := defaultConfigStore()
	if err != nil {
		return settings{}, err
	}
	return store.Resolve(requireKey)
}

func normalizeAPIBase(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", errors.New("请填写 API 地址")
	}
	if !strings.Contains(input, "://") {
		input = "https://" + input
	}
	u, err := url.Parse(input)
	if err != nil || u.Hostname() == "" || strings.ContainsAny(u.Host, " \t\r\n\\") {
		return "", errors.New("API 地址格式不正确，请填写服务商提供的网址")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("API 地址不能包含账号、密码、查询参数或 # 片段")
	}
	loopback := strings.EqualFold(u.Hostname(), "localhost")
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", errors.New("API 地址需要使用 https://；本机调试地址可使用 http://")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	for _, suffix := range []string{"/images/generations", "/images/edits", "/models"} {
		if strings.HasSuffix(u.Path, suffix) {
			u.Path = strings.TrimSuffix(u.Path, suffix)
			break
		}
	}
	if !strings.HasSuffix(u.Path, "/v1") {
		u.Path += "/v1"
	}
	u.RawPath = ""
	return u.String(), nil
}

func validateKey(key string) error {
	if key == "" {
		return errors.New("请填写 API 密钥")
	}
	if len(key) > 1024 {
		return errors.New("密钥过长，请确认粘贴的是 API Key")
	}
	for _, c := range key {
		if c <= 32 || c > 126 {
			return errors.New("密钥中含有空格、换行或非英文字符，请检查复制的内容")
		}
	}
	return nil
}

func (s *configStore) Save(base, key string) error {
	base, err := normalizeAPIBase(base)
	if err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if err := validateKey(key); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return errors.New("无法创建用户配置目录，请检查目录权限")
	}
	secret, err := s.vault.Seal(key)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			s.vault.Delete(secret)
		}
	}()
	data, _ := json.MarshalIndent(diskConfig{Version: 1, APIURL: base, Secret: secret}, "", "  ")
	file, err := os.CreateTemp(filepath.Dir(s.path), ".config-*")
	if err != nil {
		return errors.New("无法创建配置文件，请检查目录权限")
	}
	temp := file.Name()
	defer os.Remove(temp)
	err = file.Chmod(0600)
	if err == nil {
		_, err = file.Write(append(data, '\n'))
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return errors.New("无法写入配置文件，请检查磁盘空间和目录权限")
	}
	if err := replaceConfig(temp, s.path); err != nil {
		return errors.New("无法保存配置文件，请检查目录权限")
	}
	committed = true
	// Keychain references are immutable. Retaining older references avoids a
	// concurrent generation losing access after reading the previous config.
	return nil
}

func (s *configStore) Status() map[string]any {
	value, err := s.Resolve(true)
	result := map[string]any{"configured": err == nil, "source": value.Source, "api_url": value.APIURL, "storage": value.Storage, "config_path": s.path}
	if err != nil {
		result["message"] = err.Error()
	}
	return result
}

func runStatus(argv []string) error {
	if len(argv) != 0 {
		return fmt.Errorf("status 不需要参数")
	}
	store, err := defaultConfigStore()
	if err != nil {
		return err
	}
	printJSON(store.Status())
	return nil
}
