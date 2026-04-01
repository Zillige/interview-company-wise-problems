## Leetcode Company wise Problems Lists

- Curated lists of Leetcode questions group by companies, updated as of 1 June 2025.
- Each company folder consists of questions from the past 30, 60, 90 days and all time questions wherever available.

- System Design Notes: https://github.com/liquidslr/system-design-notes

## Download company logos

A helper script lives at `scripts/download_logos.go` and walks every company folder inside `data/`, requesting a PNG from `img.logo.dev` and writing it to `logos/<Company>.png`. The script writes `logos/default.png` (a small placeholder) and reuses it whenever `logo.dev` does not return a valid image.

The token for `logo.dev` can be supplied through `LOGO_DEV_TOKEN` or the `-token` flag. You can also tune the `-data-dir`, `-output-dir`, and `-concurrency` flags if your layout differs.

Example invocation:

```bash
LOGO_DEV_TOKEN=pk_cQ_2Jh4yTSyw1mlafMt_uQ \
  go run scripts/download_logos.go -data-dir data -output-dir logos
```
