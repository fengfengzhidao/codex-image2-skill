# Codex Image2 Skill

让 Codex 通过自定义 API 地址和密钥，直接调用 `gpt-image-2` 生成或编辑图片。

## 为什么做这个 Skill

最近使用 API 中转服务时，我发现不少中转站已经把 `gpt-image-2` 从常规模型列表中移出，导致 Codex 无法像以前一样直接发现并调用生图模型。

于是我写了这个 Skill。原理很简单：

1. 从环境变量读取 API 地址和密钥；
2. 直接调用 OpenAI 兼容的图片生成或编辑接口；
3. 将返回的图片保存到项目中；
4. 让 Codex 检查图片并展示最终结果。

整个 Skill 只依赖 Python 标准库，不需要安装额外的 Python 包。

## 功能

- 文生图
- 单图或多图编辑
- 可选 PNG Mask 局部编辑
- JSONL 并发批量生图
- 支持 Base64 和 URL 两种图片响应
- 自动重试网络超时、429、5xx 和 524 错误
- 输出文件覆盖保护
- API Key 脱敏，不写入 Skill 或日志

## 如何使用

这个项目包含：

- 开源项目：**codex-image2-skill**
- Skill 名称：**codex-image2**

### 1. 安装 Skill

最简单的方式是把本项目地址发给 Codex，让它帮你安装：

```text
请帮我安装这个 Skill：
https://github.com/fengfengzhidao/codex-image2-skill
```

也可以手动安装。

Windows PowerShell：

```powershell
git clone https://github.com/fengfengzhidao/codex-image2-skill.git
Copy-Item codex-image2-skill\codex-image2 "$HOME\.codex\skills\codex-image2" -Recurse
```

macOS / Linux：

```bash
git clone https://github.com/fengfengzhidao/codex-image2-skill.git
cp -R codex-image2-skill/codex-image2 ~/.codex/skills/codex-image2
```

### 2. 配置 API 地址和密钥

在 PowerShell 中执行下面两条命令，可将环境变量永久保存到当前 Windows 用户：

```powershell
[Environment]::SetEnvironmentVariable("CODEX_API_URL", "你的API地址", "User")
[Environment]::SetEnvironmentVariable("CODEX_API_KEY", "你的API密钥", "User")
```

例如，你的 API 地址可能是：

```text
https://example.com
```

既可以填写服务根地址，也可以填写以 `/v1` 结尾的地址，Skill 会自动整理接口路径。

![配置 Codex Image2 环境变量](http://image.fengfengzhidao.com/fengfeng_110920260715224031.png?key=fengfengbuzhidao)

> 配置完成后，需要完全退出并重新启动 Codex，新的环境变量才会生效。

macOS / Linux 用户可以将以下内容加入自己的 shell 配置文件：

```bash
export CODEX_API_URL="你的API地址"
export CODEX_API_KEY="你的API密钥"
```

### 3. 指定 Skill 生图

重新启动 Codex 后，在请求中指定 `$codex-image2` 即可：

```text
使用 $codex-image2 生成一张图片：
一只戴着宇航员头盔的橘猫站在月球表面，远处可以看到地球，电影感灯光。
```

![使用 Codex Image2 生图](http://image.fengfengzhidao.com/fengfeng_110920260715224141.png?key=fengfengbuzhidao)

改图示例：

```text
使用 $codex-image2 修改这张图片：
只把背景替换成雪山，人物、服装、姿势和构图保持不变。
```

## CLI 用法

通常直接在 Codex 中指定 Skill 即可，不需要手动执行 CLI。下面的命令适合调试或自动化。

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

批量任务格式和完整工作流请查看 [`codex-image2/SKILL.md`](codex-image2/SKILL.md) 和 [`batch-format.md`](codex-image2/references/batch-format.md)。

## 超简单的方式

如果觉得安装 Skill 和配置环境变量还是太麻烦，也可以直接使用我开发的网站：

### [https://ffzd.ai/](https://ffzd.ai/)

支持文生图和图生图，打开网页即可使用。

![ffzd.ai](https://image.fengfengzhidao.com/rj_102520260706151233013.png)

## 常见问题

### 配置后仍提示没有 API Key

完全退出 Codex 后重新启动。已经打开的 Codex 进程不会自动读取新设置的用户环境变量。

### 接口返回 524 或超时

这通常表示中转服务的图片生成耗时超过了网关限制。可以尝试降低质量、使用 `1024x1024`、减少批量并发，或稍后重试。

### 是否支持所有中转站

中转服务需要兼容以下接口，并提供 `gpt-image-2` 模型：

```text
POST /v1/images/generations
POST /v1/images/edits
```

不同服务的参数支持和稳定性可能存在差异。

## 安全说明

- 不要把真实 API Key 提交到 GitHub。
- 不要把 Key 写进 Skill、提示词、截图或聊天消息。
- 建议为不同服务使用独立密钥，并定期轮换。
- 本 Skill 只从 `CODEX_API_KEY` 环境变量读取密钥，不会主动保存密钥。

## License

[MIT](LICENSE)
