# AGENTS.md

This file provides guidance to coding agents when working with code in this repository.

## Workflow

- **For user-requested changes, follow this order:** make the requested edits, validate that they work, then commit.
- **Do not commit before validation passes.**
- **Any hard-won lessons should be documented in this file** (see Hard-Won Learnings below).
- **Validate real runtime behavior after changes.** After code changes that affect CLI/API behavior, run at least one real `./nanobanana generate ...` command (using a real API key) and confirm the output file is created successfully.

## Build Commands

```bash
make build          # Build the nanobanana binary
make test           # Run unit tests
make lint           # Run go vet (and golangci-lint if installed)
make install        # Build and install to ~/.local/bin
make clean          # Remove built binary
```

## Versioning & Releases

Tag and push. No files to edit.
```bash
git tag v0.1.0
git push origin main --tags
```

## Verification

### Quick verification (after code changes)
```bash
make clean && make build && make test && ./nanobanana version
```

### Full verification (requires GEMINI_API_KEY)
```bash
./nanobanana version
./nanobanana help
./nanobanana setup                                    # enter API key
./nanobanana config                                   # shows key
./nanobanana generate "a simple red circle"           # produces image
./nanobanana generate "sunset" --aspect 16:9 -o sunset.png
./nanobanana edit sunset.png "make it watercolor"     # edits image
./nanobanana generate "hi-res art" --model pro --size 4K
```

## Architecture

nanobanana is a single-binary Go CLI that generates and edits images via Google's Gemini API.

### Code Structure

All code lives in `cmd/nanobanana/main.go`:

- **Config struct** - TOML config with API key and default model
- **loadConfig/saveConfig** - Read/write `~/.config/nanobanana/config.toml`
- **resolveAPIKey** - NANOBANANA_GEMINI_API_KEY > GEMINI_API_KEY > config file
- **generateImage/editImage** - Gemini API client functions
- **Color helpers** - `success()`, `info()`, `warn()`, `errorf()` for colorful output
- **Spinner** - Simple ANSI spinner on stderr

### Key Design Decisions

- Single file keeps it auditable and simple
- Standard `flag` package (no Cobra)
- Only 2 dependencies: BurntSushi/toml + golang.org/x/term
- Models: `flash` (gemini-3.1-flash-image-preview), `pro` (gemini-3-pro-image-preview), and `legacy` (gemini-2.5-flash-image)
- API key via `x-goog-api-key` header
- Config at `~/.config/nanobanana/config.toml` (respects XDG_CONFIG_HOME)
- Flags are parsed per-subcommand

## Hard-Won Learnings

- **Gemini API uses camelCase JSON, not snake_case.** The API returns `inlineData` and `mimeType`, not `inline_data` and `mime_type`. Go struct tags must match: `json:"inlineData"`, not `json:"inline_data"`.
- **Use `generationConfig.imageConfig` for image controls; avoid `responseModalities`.** Aspect ratio and size should be sent as `generationConfig.imageConfig.aspectRatio` / `imageSize`, while leaving `responseModalities` unset to avoid empty-image responses.
- **Env var naming matches the official extension.** `NANOBANANA_GEMINI_API_KEY` is preferred (matching `gemini-cli-extensions/nanobanana`), with `GEMINI_API_KEY` as fallback.
