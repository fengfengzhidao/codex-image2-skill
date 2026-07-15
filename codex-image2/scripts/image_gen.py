#!/usr/bin/env python3
"""Generate images through a configurable OpenAI-compatible API."""

from __future__ import annotations

import argparse
import base64
import binascii
import concurrent.futures
import hashlib
import json
import mimetypes
import os
from pathlib import Path
import random
import re
import sys
import time
from typing import Any
from urllib import error, request

DEFAULT_API_URL = "https://apinebula.com"
DEFAULT_MODEL = "gpt-image-2"
DEFAULT_SIZE = "1024x1024"
DEFAULT_QUALITY = "auto"
DEFAULT_OUT_DIR = Path("output/imagegen")
RETRYABLE_STATUS = {429, 500, 502, 503, 504, 524}
QUALITY_CHOICES = ("low", "medium", "high", "auto")


class ImageGenError(RuntimeError):
    """A safe, user-facing generation error."""


def endpoint_for(base_url: str) -> str:
    base = base_url.strip().rstrip("/")
    if not base:
        base = DEFAULT_API_URL
    if base.endswith("/v1"):
        return f"{base}/images/generations"
    return f"{base}/v1/images/generations"


def edit_endpoint_for(base_url: str) -> str:
    return endpoint_for(base_url).replace("/images/generations", "/images/edits")


def prompt_from_args(args: argparse.Namespace) -> str:
    if bool(args.prompt) == bool(args.prompt_file):
        raise ImageGenError("Provide exactly one of --prompt or --prompt-file.")
    if args.prompt_file:
        try:
            prompt = Path(args.prompt_file).read_text(encoding="utf-8")
        except OSError as exc:
            raise ImageGenError(f"Could not read prompt file: {exc}") from None
    else:
        prompt = args.prompt
    prompt = prompt.strip()
    if not prompt:
        raise ImageGenError("Prompt must not be empty.")
    return prompt


def validate_n(value: Any) -> int:
    try:
        number = int(value)
    except (TypeError, ValueError):
        raise ImageGenError("n must be an integer from 1 to 10.") from None
    if not 1 <= number <= 10:
        raise ImageGenError("n must be an integer from 1 to 10.")
    return number


def validate_size(value: str) -> str:
    if value == "auto":
        return value
    match = re.fullmatch(r"(\d+)x(\d+)", value)
    if not match:
        raise ImageGenError("size must be 'auto' or WIDTHxHEIGHT.")
    width, height = map(int, match.groups())
    if width < 1 or height < 1:
        raise ImageGenError("Image dimensions must be positive.")
    return value


def extension_from_url(url: str) -> str:
    suffix = Path(url.split("?", 1)[0]).suffix.lower()
    return suffix if suffix in {".png", ".jpg", ".jpeg", ".webp"} else ".png"


def stable_name(prompt: str, extension: str = ".png") -> str:
    digest = hashlib.sha256(prompt.encode("utf-8")).hexdigest()[:12]
    return f"image-{digest}{extension}"


def output_paths(out: str | None, out_dir: str | Path, prompt: str, n: int) -> list[Path]:
    if out:
        base = Path(out)
        if not base.is_absolute() and base.parent == Path("."):
            base = Path(out_dir) / base
    else:
        base = Path(out_dir) / stable_name(prompt)
    if not base.suffix:
        base = base.with_suffix(".png")
    if n == 1:
        return [base]
    return [base.with_name(f"{base.stem}-{index}{base.suffix}") for index in range(1, n + 1)]


def ensure_writable(paths: list[Path], force: bool) -> None:
    existing = [str(path) for path in paths if path.exists()]
    if existing and not force:
        raise ImageGenError("Refusing to overwrite existing file(s): " + ", ".join(existing))


def request_json(endpoint: str, api_key: str, payload: dict[str, Any], max_attempts: int, timeout: float) -> dict[str, Any]:
    encoded = json.dumps(payload).encode("utf-8")
    last_message = "request failed"
    for attempt in range(1, max_attempts + 1):
        req = request.Request(
            endpoint,
            data=encoded,
            method="POST",
            headers={"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"},
        )
        try:
            with request.urlopen(req, timeout=timeout) as response:
                raw = response.read()
            try:
                result = json.loads(raw.decode("utf-8"))
            except (UnicodeDecodeError, json.JSONDecodeError):
                raise ImageGenError("API returned invalid JSON.") from None
            if not isinstance(result, dict):
                raise ImageGenError("API returned an unexpected JSON value.")
            return result
        except error.HTTPError as exc:
            last_message = f"API request failed with HTTP {exc.code}."
            if exc.code not in RETRYABLE_STATUS or attempt == max_attempts:
                raise ImageGenError(last_message) from None
        except (error.URLError, TimeoutError) as exc:
            reason = getattr(exc, "reason", exc)
            last_message = f"API request failed: {type(reason).__name__}."
            if attempt == max_attempts:
                raise ImageGenError(last_message) from None
        delay = min(8.0, 0.75 * (2 ** (attempt - 1))) + random.uniform(0, 0.25)
        time.sleep(delay)
    raise ImageGenError(last_message)


def multipart_body(fields: dict[str, Any], files: list[tuple[str, Path]]) -> tuple[bytes, str]:
    boundary = "codex-image2-" + os.urandom(12).hex()
    chunks: list[bytes] = []
    for name, value in fields.items():
        chunks.extend([
            f"--{boundary}\r\n".encode(),
            f'Content-Disposition: form-data; name="{name}"\r\n\r\n'.encode(),
            str(value).encode("utf-8"), b"\r\n",
        ])
    for name, path in files:
        try:
            content = path.read_bytes()
        except OSError as exc:
            raise ImageGenError(f"Could not read input image {path}: {exc}") from None
        mime = mimetypes.guess_type(path.name)[0] or "application/octet-stream"
        safe_name = path.name.replace('"', "")
        chunks.extend([
            f"--{boundary}\r\n".encode(),
            f'Content-Disposition: form-data; name="{name}"; filename="{safe_name}"\r\n'.encode(),
            f"Content-Type: {mime}\r\n\r\n".encode(), content, b"\r\n",
        ])
    chunks.append(f"--{boundary}--\r\n".encode())
    return b"".join(chunks), boundary


def request_multipart(endpoint: str, api_key: str, fields: dict[str, Any], files: list[tuple[str, Path]],
                      max_attempts: int, timeout: float) -> dict[str, Any]:
    body, boundary = multipart_body(fields, files)
    last_message = "request failed"
    for attempt in range(1, max_attempts + 1):
        req = request.Request(endpoint, data=body, method="POST", headers={
            "Authorization": f"Bearer {api_key}",
            "Content-Type": f"multipart/form-data; boundary={boundary}",
        })
        try:
            with request.urlopen(req, timeout=timeout) as response:
                raw = response.read()
            try:
                result = json.loads(raw.decode("utf-8"))
            except (UnicodeDecodeError, json.JSONDecodeError):
                raise ImageGenError("API returned invalid JSON.") from None
            if not isinstance(result, dict):
                raise ImageGenError("API returned an unexpected JSON value.")
            return result
        except error.HTTPError as exc:
            last_message = f"API request failed with HTTP {exc.code}."
            if exc.code not in RETRYABLE_STATUS or attempt == max_attempts:
                raise ImageGenError(last_message) from None
        except (error.URLError, TimeoutError) as exc:
            reason = getattr(exc, "reason", exc)
            last_message = f"API request failed: {type(reason).__name__}."
            if attempt == max_attempts:
                raise ImageGenError(last_message) from None
        delay = min(8.0, 0.75 * (2 ** (attempt - 1))) + random.uniform(0, 0.25)
        time.sleep(delay)
    raise ImageGenError(last_message)


def download_url(url: str, timeout: float) -> bytes:
    try:
        with request.urlopen(request.Request(url, headers={"User-Agent": "codex-image2/1"}), timeout=timeout) as response:
            return response.read()
    except (error.HTTPError, error.URLError, TimeoutError) as exc:
        status = getattr(exc, "code", None)
        detail = f"HTTP {status}" if status else type(getattr(exc, "reason", exc)).__name__
        raise ImageGenError(f"Could not download image URL: {detail}.") from None


def decode_images(result: dict[str, Any], timeout: float) -> list[tuple[bytes, str]]:
    data = result.get("data")
    if not isinstance(data, list) or not data:
        raise ImageGenError("API response contains no image data.")
    images: list[tuple[bytes, str]] = []
    for item in data:
        if not isinstance(item, dict):
            raise ImageGenError("API returned an invalid image entry.")
        if item.get("b64_json"):
            try:
                images.append((base64.b64decode(item["b64_json"], validate=True), ".png"))
            except (ValueError, binascii.Error):
                raise ImageGenError("API returned invalid base64 image data.") from None
        elif item.get("url"):
            url = str(item["url"])
            images.append((download_url(url, timeout), extension_from_url(url)))
        else:
            raise ImageGenError("API image entry contains neither b64_json nor url.")
    return images


def generate_job(*, prompt: str, model: str, size: str, quality: str, n: int, out: str | None,
                 out_dir: str | Path, force: bool, dry_run: bool, max_attempts: int, timeout: float) -> dict[str, Any]:
    size = validate_size(size)
    n = validate_n(n)
    if quality not in QUALITY_CHOICES:
        raise ImageGenError("quality must be low, medium, high, or auto.")
    paths = output_paths(out, out_dir, prompt, n)
    ensure_writable(paths, force)
    endpoint = endpoint_for(os.environ.get("CODEX_API_URL", DEFAULT_API_URL))
    payload = {"model": model, "prompt": prompt, "size": size, "quality": quality, "n": n}
    if dry_run:
        return {"dry_run": True, "endpoint": endpoint, "payload": payload, "outputs": [str(p.resolve()) for p in paths]}
    api_key = os.environ.get("CODEX_API_KEY", "").strip()
    if not api_key:
        raise ImageGenError("CODEX_API_KEY is not set. Set it locally, then retry.")
    result = request_json(endpoint, api_key, payload, max_attempts, timeout)
    images = decode_images(result, timeout)
    if len(images) != len(paths):
        raise ImageGenError(f"API returned {len(images)} image(s), expected {len(paths)}.")
    final_paths: list[str] = []
    for path, (content, actual_extension) in zip(paths, images):
        if not path.suffix:
            path = path.with_suffix(actual_extension)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)
        final_paths.append(str(path.resolve()))
    return {"model": model, "size": size, "quality": quality, "outputs": final_paths}


def edit_job(*, prompt: str, images: list[str], mask: str | None, model: str, size: str, quality: str,
             n: int, out: str | None, out_dir: str | Path, force: bool, dry_run: bool,
             max_attempts: int, timeout: float) -> dict[str, Any]:
    size = validate_size(size)
    n = validate_n(n)
    if quality not in QUALITY_CHOICES:
        raise ImageGenError("quality must be low, medium, high, or auto.")
    image_paths = [Path(item) for item in images]
    missing = [str(path) for path in image_paths if not path.is_file()]
    if mask and not Path(mask).is_file():
        missing.append(mask)
    if missing:
        raise ImageGenError("Input file(s) not found: " + ", ".join(missing))
    paths = output_paths(out, out_dir, prompt, n)
    ensure_writable(paths, force)
    endpoint = edit_endpoint_for(os.environ.get("CODEX_API_URL", DEFAULT_API_URL))
    fields = {"model": model, "prompt": prompt, "size": size, "quality": quality, "n": n}
    if dry_run:
        return {
            "dry_run": True, "endpoint": endpoint, "fields": fields,
            "images": [str(path.resolve()) for path in image_paths],
            "mask": str(Path(mask).resolve()) if mask else None,
            "outputs": [str(path.resolve()) for path in paths],
        }
    api_key = os.environ.get("CODEX_API_KEY", "").strip()
    if not api_key:
        raise ImageGenError("CODEX_API_KEY is not set. Set it locally, then retry.")
    files = [("image", path) for path in image_paths]
    if mask:
        files.append(("mask", Path(mask)))
    result = request_multipart(endpoint, api_key, fields, files, max_attempts, timeout)
    decoded = decode_images(result, timeout)
    if len(decoded) != len(paths):
        raise ImageGenError(f"API returned {len(decoded)} image(s), expected {len(paths)}.")
    final_paths: list[str] = []
    for path, (content, _) in zip(paths, decoded):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)
        final_paths.append(str(path.resolve()))
    return {"model": model, "size": size, "quality": quality, "outputs": final_paths}


def run_generate(args: argparse.Namespace) -> int:
    prompt = prompt_from_args(args)
    result = generate_job(
        prompt=prompt, model=args.model, size=args.size, quality=args.quality, n=args.n,
        out=args.out, out_dir=args.out_dir, force=args.force, dry_run=args.dry_run,
        max_attempts=args.max_attempts, timeout=args.timeout,
    )
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


def run_edit(args: argparse.Namespace) -> int:
    prompt = prompt_from_args(args)
    result = edit_job(
        prompt=prompt, images=args.image, mask=args.mask, model=args.model, size=args.size,
        quality=args.quality, n=args.n, out=args.out, out_dir=args.out_dir, force=args.force,
        dry_run=args.dry_run, max_attempts=args.max_attempts, timeout=args.timeout,
    )
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


def load_jobs(path: str) -> list[dict[str, Any]]:
    jobs: list[dict[str, Any]] = []
    try:
        lines = Path(path).read_text(encoding="utf-8").splitlines()
    except OSError as exc:
        raise ImageGenError(f"Could not read batch input: {exc}") from None
    for line_number, line in enumerate(lines, 1):
        if not line.strip():
            continue
        try:
            job = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ImageGenError(f"Invalid JSON on line {line_number}: {exc.msg}.") from None
        if not isinstance(job, dict) or not str(job.get("prompt", "")).strip():
            raise ImageGenError(f"Line {line_number} must be an object with a non-empty prompt.")
        jobs.append(job)
    if not jobs:
        raise ImageGenError("Batch input contains no jobs.")
    return jobs


def run_batch(args: argparse.Namespace) -> int:
    jobs = load_jobs(args.input)
    allowed = {"prompt", "model", "size", "quality", "n", "out"}
    results: list[dict[str, Any] | None] = [None] * len(jobs)

    def execute(index: int, job: dict[str, Any]) -> tuple[int, dict[str, Any]]:
        unknown = set(job) - allowed
        if unknown:
            raise ImageGenError("Unsupported batch field(s): " + ", ".join(sorted(unknown)))
        result = generate_job(
            prompt=str(job["prompt"]).strip(), model=str(job.get("model", args.model)),
            size=str(job.get("size", args.size)), quality=str(job.get("quality", args.quality)),
            n=job.get("n", args.n), out=job.get("out"), out_dir=args.out_dir,
            force=args.force, dry_run=args.dry_run, max_attempts=args.max_attempts, timeout=args.timeout,
        )
        return index, {"index": index + 1, "ok": True, **result}

    with concurrent.futures.ThreadPoolExecutor(max_workers=args.concurrency) as pool:
        future_map = {pool.submit(execute, index, job): index for index, job in enumerate(jobs)}
        for future in concurrent.futures.as_completed(future_map):
            index = future_map[future]
            try:
                _, results[index] = future.result()
            except Exception as exc:
                message = str(exc) if isinstance(exc, ImageGenError) else f"Unexpected {type(exc).__name__}."
                results[index] = {"index": index + 1, "ok": False, "error": message}
                if args.fail_fast:
                    for pending in future_map:
                        pending.cancel()
    complete = [result for result in results if result is not None]
    failures = sum(not bool(result["ok"]) for result in complete)
    print(json.dumps({"jobs": complete, "succeeded": len(complete) - failures, "failed": failures}, ensure_ascii=False, indent=2))
    return 1 if failures else 0


def add_common(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--model", default=DEFAULT_MODEL)
    parser.add_argument("--size", default=DEFAULT_SIZE)
    parser.add_argument("--quality", choices=QUALITY_CHOICES, default=DEFAULT_QUALITY)
    parser.add_argument("--n", type=int, default=1)
    parser.add_argument("--out-dir", default=str(DEFAULT_OUT_DIR))
    parser.add_argument("--force", action="store_true")
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--max-attempts", type=int, default=3)
    parser.add_argument("--timeout", type=float, default=150.0)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    generate = subparsers.add_parser("generate", help="Generate one prompt")
    generate.add_argument("--prompt")
    generate.add_argument("--prompt-file")
    generate.add_argument("--out")
    add_common(generate)
    generate.set_defaults(func=run_generate)
    edit = subparsers.add_parser("edit", help="Edit one or more input images")
    edit.add_argument("--image", action="append", required=True, help="Input image; repeat for multiple images")
    edit.add_argument("--mask", help="Optional PNG mask")
    edit.add_argument("--prompt")
    edit.add_argument("--prompt-file")
    edit.add_argument("--out")
    add_common(edit)
    edit.set_defaults(func=run_edit)
    batch = subparsers.add_parser("generate-batch", help="Generate JSONL jobs concurrently")
    batch.add_argument("--input", required=True)
    batch.add_argument("--concurrency", type=int, default=2)
    batch.add_argument("--fail-fast", action="store_true")
    add_common(batch)
    batch.set_defaults(func=run_batch)
    return parser


def main() -> int:
    try:
        args = build_parser().parse_args()
        if args.max_attempts < 1:
            raise ImageGenError("--max-attempts must be at least 1.")
        if args.timeout <= 0:
            raise ImageGenError("--timeout must be positive.")
        if hasattr(args, "concurrency") and args.concurrency < 1:
            raise ImageGenError("--concurrency must be at least 1.")
        return args.func(args)
    except ImageGenError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
