# Batch JSONL format

Write one JSON object per line. Each job requires `prompt` and may override generation or output settings.

```jsonl
{"prompt":"A blue ceramic mug on white","out":"mug.png"}
{"prompt":"A red paper kite in a clear sky","size":"1536x1024","quality":"low","n":2,"out":"kite.png"}
```

Supported fields are `prompt`, `size`, `quality`, `n`, `out`, and `model`. Relative `out` paths are resolved under `--out-dir`. Blank lines are ignored.

Use unique output names. When `n` is greater than one, the CLI adds `-1`, `-2`, and so on before the extension. The batch command prints a JSON summary and exits nonzero if any job fails.
