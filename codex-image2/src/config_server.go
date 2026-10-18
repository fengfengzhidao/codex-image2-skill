package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

//go:embed configure.html
var configureHTML string

type configureLaunch struct {
	URL           string `json:"url"`
	ExpiresIn     int    `json:"expires_in_seconds"`
	BrowserOpened bool   `json:"browser_opened"`
	Message       string `json:"message"`
}

// The short-lived parent returns immediately to Codex. Its detached child has
// no inherited console or log files and shuts down after 15 minutes.
func runConfigure(argv []string) error {
	fs := flag.NewFlagSet("configure", flag.ContinueOnError)
	noBrowser := fs.Bool("no-browser", false, "只输出配置页地址，不自动打开浏览器")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("configure 不接受密钥参数，请在配置页输入")
	}
	exe, err := os.Executable()
	if err != nil {
		return errors.New("无法找到当前程序")
	}
	cmd := exec.Command(exe, "_configure-server")
	detach(cmd)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return errors.New("无法启动配置页")
	}
	defer pipe.Close()
	if err := cmd.Start(); err != nil {
		return errors.New("无法启动配置页，请检查程序是否被系统拦截")
	}
	ready := make(chan configureLaunch, 1)
	go func() {
		var value configureLaunch
		_ = json.NewDecoder(io.LimitReader(pipe, 8192)).Decode(&value)
		ready <- value
	}()
	var result configureLaunch
	select {
	case result = <-ready:
	case <-time.After(10 * time.Second):
	}
	if result.URL == "" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return errors.New("配置页启动失败，请确认本机允许程序监听回环网络端口")
	}
	_ = cmd.Process.Release()
	if !*noBrowser {
		result.BrowserOpened = openBrowser(result.URL) == nil
	}
	result.Message = "请在本地配置页填写信息。保存后立即生效，无需重启 Codex；页面在 15 分钟后失效。"
	if !result.BrowserOpened {
		result.Message += " 请用浏览器打开 url 字段中的地址。"
	}
	printJSON(result)
	return nil
}

type configurationServer struct {
	store    *configStore
	host     string
	token    string
	shutdown func()
	mu       sync.Mutex
}

func serveConfiguration() error {
	store, err := defaultConfigStore()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return errors.New("无法启动本地配置服务")
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	ui := &configurationServer{store: store, host: listener.Addr().String(), token: randomID(), shutdown: cancel}
	server := &http.Server{Handler: ui, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 180 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
	go func() {
		<-ctx.Done()
		stopCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = server.Shutdown(stopCtx)
	}()
	printJSON(configureLaunch{URL: "http://" + ui.host + "/#" + ui.token, ExpiresIn: 900})
	_ = os.Stdout.Close() // Parent can finish; no credentials are ever printed.
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func writeConfigJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *configurationServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	if r.Host != s.host {
		http.Error(w, "无效的本机地址", http.StatusForbidden)
		return
	}
	if r.URL.Path == "/" && r.Method == http.MethodGet {
		nonce := randomID()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; style-src 'nonce-"+nonce+"'; connect-src 'self'; img-src data:; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
		_, _ = io.WriteString(w, strings.ReplaceAll(configureHTML, "{{NONCE}}", nonce))
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.token)) != 1 {
		writeConfigJSON(w, http.StatusUnauthorized, map[string]string{"message": "配置链接无效或已过期，请回到 Codex 重新打开配置页"})
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/status" {
		s.mu.Lock()
		defer s.mu.Unlock()
		writeConfigJSON(w, http.StatusOK, s.store.Status())
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "不支持此操作", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Origin") != "http://"+s.host || r.Header.Get("Content-Type") != "application/json" {
		writeConfigJSON(w, http.StatusForbidden, map[string]string{"message": "请使用本机配置页提交"})
		return
	}
	if r.URL.Path == "/api/close" {
		writeConfigJSON(w, http.StatusOK, map[string]string{"message": "配置页已关闭，可以返回 Codex"})
		s.shutdown()
		return
	}
	if r.URL.Path == "/api/reveal-key" {
		// Only an explicit action in the authenticated local page can reveal a
		// stored key. Status, CLI output and initial HTML never contain it.
		s.mu.Lock()
		current, err := s.store.Resolve(true)
		s.mu.Unlock()
		if err != nil {
			writeConfigJSON(w, http.StatusBadRequest, map[string]string{"message": "无法显示已保存的密钥，请重新填写或解锁系统凭据存储"})
			return
		}
		writeConfigJSON(w, http.StatusOK, map[string]string{"api_key": current.APIKey, "api_url": current.APIURL})
		return
	}
	var input struct {
		APIURL            string `json:"api_url"`
		APIKey            string `json:"api_key"`
		ConfirmPaid       bool   `json:"confirm_paid"`
		ConfirmUnverified bool   `json:"confirm_unverified"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		writeConfigJSON(w, http.StatusBadRequest, map[string]string{"message": "配置内容格式不正确或过长"})
		return
	}
	base, err := normalizeAPIBase(input.APIURL)
	key := strings.TrimSpace(input.APIKey)
	s.mu.Lock()
	if err == nil && key == "" {
		current, loadErr := s.store.Resolve(true)
		if loadErr != nil {
			err = errors.New("请重新填写 API 密钥")
		} else if current.APIURL != base {
			err = errors.New("切换 API 地址时，请重新粘贴对应服务的密钥")
		} else {
			key = current.APIKey
		}
	}
	if err == nil {
		err = validateKey(key)
	}
	if err != nil {
		s.mu.Unlock()
		writeConfigJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	s.mu.Unlock()
	if r.URL.Path == "/api/save" {
		result := checkConnection(r.Context(), base, key)
		result.APIURL = base
		if result.Level == "error" {
			result.Message += "。配置未保存，原配置保持不变。"
			writeConfigJSON(w, http.StatusUnprocessableEntity, result)
			return
		}
		if !result.OK && !input.ConfirmUnverified {
			result.RequiresConfirmation = true
			result.Message += "\n配置尚未保存。请检查密钥的模型权限；确认服务支持图片接口后，可以选择“仍然保存”。"
			writeConfigJSON(w, http.StatusOK, result)
			return
		}
		s.mu.Lock()
		err = s.store.Save(base, key)
		s.mu.Unlock()
		if err != nil {
			writeConfigJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
			return
		}
		result.Saved = true
		if result.OK {
			result.Message = "配置已保存，将使用 " + result.Model + "。实际生图仍以图片接口结果为准，无需重启 Codex。"
		} else {
			result.Message = "配置已按你的选择保存，但尚未确认生图模型权限。\n" + result.Message
		}
		writeConfigJSON(w, http.StatusOK, result)
		return
	}
	switch r.URL.Path {
	case "/api/test":
		result := checkConnection(r.Context(), base, key)
		writeConfigJSON(w, http.StatusOK, result)
	case "/api/test-image":
		if !input.ConfirmPaid {
			writeConfigJSON(w, http.StatusBadRequest, map[string]string{"message": "请先确认生图测试可能产生费用"})
			return
		}
		writeConfigJSON(w, http.StatusOK, checkImage(r.Context(), base, key))
	default:
		http.NotFound(w, r)
	}
}

type connectionResult struct {
	OK                   bool     `json:"ok"`
	Level                string   `json:"level"`
	Message              string   `json:"message"`
	Image                string   `json:"image,omitempty"`
	Model                string   `json:"model,omitempty"`
	Models               []string `json:"models"`
	ModelsChecked        bool     `json:"models_checked"`
	RequiresConfirmation bool     `json:"requires_confirmation,omitempty"`
	Saved                bool     `json:"saved,omitempty"`
	APIURL               string   `json:"api_url,omitempty"`
}

func diagnosticClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func networkResult(err error) connectionResult {
	var timeout net.Error
	message := "无法连接服务，请检查网络、代理或 API 地址"
	if errors.As(err, &timeout) && timeout.Timeout() {
		message = "连接超时，请检查网络或稍后重试"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		message = "找不到这个服务地址，请检查网址拼写和网络连接"
	}
	// Do not echo errors or response bodies: providers can reflect credentials.
	return connectionResult{Level: "error", Message: message}
}

func httpResult(status int) connectionResult {
	message := fmt.Sprintf("服务返回 HTTP %d，请检查服务商的接口说明", status)
	level := "error"
	switch status {
	case 401:
		message = "密钥未通过验证，请确认密钥有效且属于当前服务"
	case 403:
		message = "服务拒绝访问，请检查密钥权限或联系服务商"
	case 402:
		message = "服务要求付费，请检查账户余额或联系服务商"
	case 404, 405, 501:
		message = "服务未提供此检测接口，仍需通过实际生图确认图片能力"
		level = "warning"
	case 429:
		message = "请求过于频繁或额度受限，请稍后重试并检查服务额度"
	case 301, 302, 303, 307, 308:
		message = "此地址发生跳转。为保护密钥未继续请求，请填写服务商的最终 API 地址"
	default:
		if status >= 500 {
			message = "服务商暂时出错，请稍后重试"
		}
	}
	return connectionResult{Level: level, Message: message}
}

func checkConnection(ctx context.Context, base, key string) connectionResult {
	ids, status, err := fetchModelList(ctx, base, key, 20*time.Second)
	if err != nil && status == 0 {
		return networkResult(err)
	}
	if status < 200 || status >= 300 {
		return httpResult(status)
	}
	if err != nil {
		return connectionResult{Level: "warning", Message: "地址可以访问，但没有返回兼容的模型列表。请确认 API 地址，并通过实际生图验证"}
	}
	ids = displayModelIDs(ids, key)
	if !hasImageModel(ids) {
		return connectionResult{Level: "warning", Message: "当前密钥的模型列表未包含 gpt-image-2 或 gpt-image-2.5，请检查模型权限或更换密钥。部分服务可能隐藏图片模型。", Models: ids, ModelsChecked: true}
	}
	model := listedImageModel(ids)
	message := "模型列表接口访问成功，已发现 gpt-image-2。实际生图仍以图片接口结果为准。"
	if model == preferredModel {
		message = "模型列表接口访问成功，已发现 gpt-image-2.5，将优先使用。实际生图仍以图片接口结果为准。"
	}
	return connectionResult{OK: true, Level: "success", Message: message, Model: model, Models: ids, ModelsChecked: true}
}

func checkImage(ctx context.Context, base, key string) connectionResult {
	model := (&imageModelSelector{}).Select(ctx, settings{APIURL: base, APIKey: key}, "auto", false, 8*time.Second)
	payload, _ := json.Marshal(map[string]any{"model": model, "prompt": "A small blue circle on a plain white background.", "size": "1024x1024", "quality": "low", "n": 1})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(base, "generations"), bytes.NewReader(payload))
	if err != nil {
		return networkResult(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	client := diagnosticClient(150 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return networkResult(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result := httpResult(resp.StatusCode)
		if resp.StatusCode == 404 {
			result.Level = "error"
			result.Message = "找不到图片生成接口，请检查 API 地址和服务商的图片接口支持情况"
		}
		return result
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024+1))
	if err != nil || len(raw) > 32*1024*1024 {
		return connectionResult{Level: "error", Message: "图片响应读取失败或超过大小限制"}
	}
	var result apiResponse
	if json.Unmarshal(raw, &result) != nil || len(result.Data) == 0 {
		return connectionResult{Level: "error", Message: "图片接口未返回兼容的图片数据，请联系服务商"}
	}
	// Never send a provider-controlled URL to the browser (it could expose the
	// local session via another origin). Inline only recognized raster formats.
	if result.Data[0].B64JSON != "" {
		data, err := base64.StdEncoding.DecodeString(result.Data[0].B64JSON)
		if err != nil {
			return connectionResult{Level: "error", Message: "服务返回的图片编码无效"}
		}
		mime := http.DetectContentType(data)
		if mime == "image/png" || mime == "image/jpeg" || mime == "image/webp" {
			return connectionResult{OK: true, Level: "success", Message: "测试生图成功，使用模型：" + model + "。", Model: model, Image: "data:" + mime + ";base64," + result.Data[0].B64JSON}
		}
		return connectionResult{Level: "error", Message: "服务返回的数据不是支持的图片格式"}
	}
	if result.Data[0].URL != "" {
		return connectionResult{OK: true, Level: "success", Message: "图片接口已返回图片链接，使用模型：" + model + "。可以返回 Codex 正式生图。", Model: model}
	}
	return connectionResult{Level: "error", Message: "图片接口未返回图片数据"}
}
