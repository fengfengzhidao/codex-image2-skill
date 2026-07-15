# Codex Image2 Skill

通过可配置的 OpenAI 兼容 Image API，在 Codex 中使用 `gpt-image-2` 生成或编辑图片。

## 功能

- 单图生成
- JSONL 并发批量生成
- 单图或多图编辑
- 可选 PNG mask 局部编辑
- 自动处理 Base64 或 URL 图片响应
- 对超时、429、5xx 和 524 进行有限重试
- 只依赖 Python 标准库

## 安装

将仓库中的 `codex-image2` 目录复制到 Codex Skills 目录。

Windows PowerShell：

```powershell
git clone git@github.com:fengfengzhidao/codex-image2-skill.git
Copy-Item codex-image2-skill\codex-image2 "$HOME\.codex\skills\codex-image2" -Recurse
```

macOS / Linux：

```bash
git clone git@github.com:fengfengzhidao/codex-image2-skill.git
cp -R codex-image2-skill/codex-image2 ~/.codex/skills/codex-image2
```

重新启动 Codex，然后通过 `$codex-image2` 使用。

## 配置

默认 API 地址为 `https://apinebula.com`。永久设置当前 Windows 用户的环境变量：

```powershell
[Environment]::SetEnvironmentVariable("CODEX_API_URL", "https://apinebula.com", "User")
[Environment]::SetEnvironmentVariable("CODEX_API_KEY", "你的 API Key", "User")
```

设置后完全退出并重新启动 Codex。不要将真实 Key 写入仓库、命令示例、截图或聊天消息。

macOS / Linux 可将以下内容加入 shell 配置文件：

```bash
export CODEX_API_URL="https://apinebula.com"
export CODEX_API_KEY="your-api-key"
```

## CLI 示例

生成图片：

```powershell
python codex-image2/scripts/image_gen.py generate `
  --prompt "A tiny blue nebula inside a glass bottle" `
  --quality auto `
  --out "output/imagegen/nebula.png"
```

编辑图片：

```powershell
python codex-image2/scripts/image_gen.py edit `
  --image "input.png" `
  --prompt "Replace only the background with a warm studio backdrop" `
  --out "output/imagegen/edited.png"
```

批量格式和更多工作流说明见 `codex-image2/SKILL.md` 与 `codex-image2/references/batch-format.md`。

## 安全说明

Skill 仅从 `CODEX_API_KEY` 环境变量读取密钥，不会将密钥保存到配置文件或输出日志。建议为不同服务使用独立密钥并定期轮换。
