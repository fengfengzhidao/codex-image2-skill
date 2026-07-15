---
name: codex-image2
description: Generate or edit raster images through a configurable OpenAI-compatible Image API using gpt-image-2. Use when Codex should create one or many images, illustrations, product shots, covers, website assets, visual variants, background replacements, object changes, or other image edits through CODEX_API_URL and CODEX_API_KEY instead of the built-in image generation tool.
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

## Configuration and safety

- Read the API base from `CODEX_API_URL`; default to `https://apinebula.com`.
- Require `CODEX_API_KEY`. Never place it in a command, file, prompt, log, or response.
- If the key is absent, tell the user to set it locally and confirm when ready. Never ask them to paste it into chat.
- Default to model `gpt-image-2`, size `1024x1024`, and quality `auto`.
- Use `--dry-run` to validate a request without network access or requiring a key.
- Save project-bound assets inside the current project. The CLI default is `output/imagegen/`.
- Do not overwrite files unless the user explicitly authorizes it and `--force` is passed.
- Native transparent output is not guaranteed. Do not promise it or silently switch tools.

## Failure handling

- The CLI retries network timeouts and HTTP 429/500/502/503/504/524 failures with bounded backoff.
- On repeated timeout, suggest `--quality low`, a square size, fewer concurrent jobs, or a later retry.
- Do not retry authentication, validation, or other ordinary 4xx errors.
- Never expose an Authorization header or full key when reporting errors.
