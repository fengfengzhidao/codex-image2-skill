---
name: codex-image2
description: Configure a custom OpenAI-compatible image service with a local setup page, or generate and edit images through that service with automatic preference for gpt-image-2.5 when listed, falling back to gpt-image-2. Use for codex-image2 setup, API configuration, text-to-image, image edits, and batch image generation.
---

# Codex Image2

Generate images with the bundled native CLI. Prefer this skill's executable over the built-in `image_gen` tool whenever this skill is active. No Python, Node.js, Go, or package installation is required.

## Select the executable

Choose once from the current operating system and CPU architecture:

- Windows x64: `bin/codex-image2-windows-amd64.exe`
- Windows ARM64: `bin/codex-image2-windows-arm64.exe`
- macOS Intel: `bin/codex-image2-darwin-amd64`
- macOS Apple Silicon: `bin/codex-image2-darwin-arm64`

On macOS, run `chmod +x <executable>` if execute permission was not preserved. Do not compile from source during normal use.

## Workflow

For setup requests, use the configuration workflow below. Before the first image call in a session, run `status`; if unconfigured, open `configure` and let the user finish in their browser before generating.

1. Decide whether the request is a new image, an edit, or multiple distinct assets/variants.
2. Collect the prompt, intended use, exact text, visual constraints, and avoid items.
3. Shape the prompt only as much as needed. Preserve detailed prompts; tastefully clarify generic prompts without inventing brands, people, slogans, or unrelated objects.
4. Run the selected executable with `generate` for one prompt, `edit` for changes to existing images, or `generate-batch` for JSONL jobs.
5. Inspect each output for subject, composition, text accuracy, constraints, and visible artifacts.
6. If revision is needed, change one targeted aspect per iteration and re-check.
7. Report absolute output paths, the final prompt or prompt set, size, quality, and model.

## Prompt structure

Use only relevant lines:

```text
Asset type: <where the image will be used>
Primary request: <the user's request>
Scene/backdrop: <environment>
Subject: <main subject>
Style/medium: <photo, illustration, 3D, etc.>
Composition/framing: <camera angle, crop, placement, negative space>
Lighting/mood: <lighting and mood>
Color palette: <palette notes>
Text (verbatim): "<exact text>"
Constraints: <must keep or include>
Avoid: <must not include>
```

Do not add detail merely to fill the schema. For text in images, quote it verbatim and request exact rendering.

## Generate one image

```powershell
& "<skill-dir>\bin\codex-image2-windows-amd64.exe" generate `
  --prompt "A small blue nebula in a glass bottle, studio product photo" `
  --size 1024x1024 `
  --quality auto `
  --out "output/imagegen/nebula.png"
```

Use `--prompt-file` for long prompts. Use `--n` only for variants of the same prompt. Distinct assets belong in separate calls or a batch.

## Edit an image

Inspect each input image before editing. State its role and repeat invariants in the prompt so unrelated details do not drift.

```powershell
& "<skill-dir>\bin\codex-image2-windows-amd64.exe" edit `
  --image "input/product.png" `
  --prompt "Replace only the background with a warm studio backdrop. Keep the product, label, proportions, and edges unchanged." `
  --quality auto `
  --out "output/imagegen/product-edited.png"
```

Repeat `--image` for multiple reference or compositing inputs. Use `--mask mask.png` for a localized edit when a compatible PNG mask is available. Preserve originals and always write edits to a new output path.

## Generate a batch

Read [references/batch-format.md](references/batch-format.md) before preparing a batch. Then run:

```powershell
& "<skill-dir>\bin\codex-image2-windows-amd64.exe" generate-batch `
  --input "tmp/imagegen/jobs.jsonl" `
  --out-dir "output/imagegen" `
  --concurrency 2
```

## Configure the image service

- Run `<executable> status` to inspect `configured`, `source`, `api_url`, and `message`. It never returns a key.
- `configured` only confirms that local credentials are readable; it does not prove remote authentication or image permissions. Do not claim API access was verified from `status` alone.
- For first use, missing/inaccessible credentials, or a request to change settings, run `<executable> configure`. This launches a detached local configuration page and returns immediately. Do not background it with shell scripts or wait for its server to exit.
- Tell the user to fill in the API address and key in the browser and click **保存配置**. If the browser did not open, provide the returned local `url` as a clickable link. This temporary link is valid for 15 minutes on the user's machine.
- Saving first queries the submitted key's model list. Listed `gpt-image-2` or `gpt-image-2.5` allows a normal save. If neither is listed, or the list is unsupported/incompatible, the page warns and leaves the old configuration unchanged; only the user's explicit **仍然保存** action saves an unverified service. Authentication/network/rate-limit/server errors block saving even with this confirmation. Never click the override for the user.
- Never ask for the key in chat, put it into a command, or fill it through automation. The user enters it directly in the local page; only that program handles it.
- After the user says they saved, run `status` again and continue the original image request if configured. Do not repeatedly reopen the page or poll while waiting for user input.
- Saved settings take effect on the next CLI invocation, with no Codex restart. Existing local config takes priority over legacy environment variables as a complete address/key pair. A damaged local config must be repaired through the setup page rather than silently falling back to another service.
- **检测连接** only probes the model-list endpoint and shows the current key's model IDs, highlighting image models and the automatic selection. Missing or hidden models trigger a warning rather than proof that the image API is unavailable. Editing the address/key clears stale results. The optional **测试生图（可能计费）** requires user confirmation and makes one image request without retries. Do not run paid tests automatically as part of setup.
- Windows encrypts the key with current-user DPAPI; macOS stores it in the login Keychain. macOS may ask the user to unlock or allow access to the Keychain.
- The user can click **显示** to reveal a saved key in their local page. Hiding clears a revealed saved key, and the page automatically hides it after 30 seconds or when the page becomes hidden. Never click reveal, inspect a real revealed key, or call the reveal endpoint through agent tools.

## Configuration and safety

- Let the CLI read credentials. Never inspect credential files or credential-store contents yourself, or copy keys into project files, prompts, logs, or responses. The configuration page may save keys only through the CLI's platform credential backend.
- Legacy `CODEX_API_URL` and `CODEX_API_KEY` remain supported when no local configuration exists; their URL defaults to `https://apinebula.ai/v1`. Recommend the local setup page to new users. Existing saved URLs are preserved.
- Default to `--model auto` (or omit the flag), size `1024x1024`, and quality `auto`. The CLI checks the configured service's model list and prefers `gpt-image-2.5` when listed; otherwise it calls `gpt-image-2` directly. An unsupported, hidden, invalid or unreachable model list does not block this fallback. Each batch shares discovery per service/credential pair.
- Respect an explicitly requested model using `--model <name>` or a batch row's `model`; do not add `--model gpt-image-2` by habit, as that overrides automatic preference. Report the actual model from the CLI result.
- Dry runs never query model availability; `model_selection: auto` indicates automatic discovery was skipped, and their example payload uses `gpt-image-2`.
- Use `--dry-run` to validate a request without network access or requiring a key.
- Save project-bound assets inside the current project. The CLI default is `output/imagegen/`.
- Do not overwrite files unless the user explicitly authorizes it and `--force` is passed.
- Native transparent output is not guaranteed. Do not promise it or silently switch tools.

## Failure handling

- The CLI retries network timeouts and HTTP 429/500/502/503/504/524 failures with bounded backoff.
- On repeated timeout, suggest `--quality low`, a square size, fewer concurrent jobs, or a later retry.
- Do not retry authentication, validation, or other ordinary 4xx errors.
- Never expose an Authorization header or full key when reporting errors.
