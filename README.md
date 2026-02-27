# nanobanana

**Generate and edit images with Gemini from the command line.**

A fast, single-binary CLI for Google's Gemini image generation models. Text-to-image and image editing.

## Quick Start

```bash
# Install via Homebrew (macOS/Linux)
brew install finbarr/tap/nanobanana-cli

# Or install via Go
go install github.com/finbarr/nanobanana-cli/cmd/nanobanana@latest
```

Set up your API key:

```bash
nanobanana setup
# Or set the environment variable
export NANOBANANA_GEMINI_API_KEY="your-key-here"
```

Get a Gemini API key from [Google AI Studio](https://aistudio.google.com/apikey).

Then generate:

```bash
nanobanana generate "a cat in space"
```

## Commands

```bash
nanobanana generate "prompt"          # Generate an image (alias: gen)
nanobanana edit photo.jpg "prompt"    # Edit an existing image (use - for stdin)
nanobanana setup                      # Configure API key
nanobanana config                     # Show current configuration
nanobanana version                    # Show version
nanobanana upgrade                    # Upgrade to latest version
nanobanana readme                     # Print full docs as markdown (for LLMs/agents)
nanobanana help                       # Show help
```

## Examples

```bash
# Basic generation
nanobanana generate "a cat in space"

# Widescreen with custom output path
nanobanana generate --aspect 16:9 --output sunset.png "sunset over mountains"

# Use the pro model
nanobanana generate --model pro "a photorealistic forest"

# Aspect and size can also just go in the prompt
nanobanana generate "a 4K panoramic sunset in 21:9 aspect ratio"

# Generate 4 variations
nanobanana generate --count 4 "logo ideas for a coffee shop"

# JSON output for scripts and agents
nanobanana generate --json "a simple icon"
# → {"file":"nanobanana_20260212_120000.png","model":"gemini-3.1-flash-image-preview","prompt":"a simple icon","bytes":45678}

# Open image immediately after generating
nanobanana generate --preview "a blue sky"

# Nano Banana 2 supports 512px output
nanobanana generate --size 512px "an app icon"

# Edit an existing image
nanobanana edit photo.jpg "make it look like a watercolor painting"
nanobanana edit --preview photo.jpg "remove the background"

# Piping: use - for stdin input and -o - for stdout output
nanobanana generate -o - "a red circle" | nanobanana edit -o result.png - "make it blue"

# Quiet mode for scripting (prints only file path)
nanobanana gen -q "logo" | xargs open
```

## Flags

Flags go after the subcommand and before positional args: `nanobanana generate --flag "prompt"`.

For `edit` and `generate`, this CLI uses Go's standard flag parsing, which stops at the first positional argument.

```bash
# Correct: flags before positional args
nanobanana edit --model flash --size 512px input.png "turn this into pixel art"

# Incorrect: flags after positional args (treated as prompt text)
nanobanana edit input.png --model flash "turn this into pixel art"
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--model` | `-m` | `flash` | Model: `flash`, `pro`, `legacy`, or a full model name |
| `--output` | `-o` | auto | Output file path (`-` for stdout) |
| `--aspect` | `-a` | `1:1` | Aspect ratio: `1:1`, `2:3`, `3:2`, `3:4`, `4:3`, `4:5`, `5:4`, `9:16`, `16:9`, `21:9` (`flash` also supports `1:4`, `1:8`, `4:1`, `8:1`) |
| `--size` | `-s` | `1K` | Size: `1K`, `2K`, `4K` (`flash` also supports `512px`; `legacy` supports only `1K`) |
| `--count` | `-n` | `1` | Number of images to generate (1-8, `generate` only) |
| `--quiet` | `-q` | | Suppress output, print only file path to stdout |
| `--json` | | | Output result as JSON to stdout |
| `--preview` | `-p` | | Open image after saving |

**Note on `--aspect` and `--size`:** These map to Gemini's native `generationConfig.imageConfig` fields (`aspectRatio` and `imageSize`). You can still describe dimensions in prompt text when needed.

## Models

| Alias | Model ID | Notes |
|-------|----------|-------|
| `flash` | `gemini-3.1-flash-image-preview` | Nano Banana 2. Default. |
| `pro` | `gemini-3-pro-image-preview` | Nano Banana Pro. |
| `legacy` | `gemini-2.5-flash-image` | Older flash image model. |

You can also pass any full Gemini model name directly (e.g., `--model gemini-3.1-flash-image-preview`).

## Configuration

Run `nanobanana setup` to save your API key and default model.

Settings are saved to `~/.config/nanobanana/config.toml` (respects `XDG_CONFIG_HOME`; uses `~/Library/Application Support/` on macOS):

```toml
api_key = "AIza..."
model = "flash"
```

### Environment Variables

| Variable | Description |
|----------|-------------|
| `NANOBANANA_GEMINI_API_KEY` | API key (preferred, matches official Gemini extension) |
| `GEMINI_API_KEY` | API key (fallback) |
| `NANOBANANA_MODEL` | Default model (overrides config file) |

Priority: CLI flags > env vars > config file > defaults.

## Development

### Building

```bash
make build          # Build binary
make test           # Run tests
make lint           # Run linters
make install        # Install to ~/.local/bin
```

### Versioning

Version is derived automatically from git tags via `git describe`:
- Tagged commit: `v0.1.0`
- After tag: `v0.1.0-3-gabcdef1` (3 commits after tag)
- Uncommitted changes: adds `-dirty`

**No files to edit for releases.** The Makefile handles it.

### Releasing

To release a new version:

```bash
git tag v0.1.0
git push origin main --tags
```

GitHub Actions will automatically:
1. Build binaries for linux/darwin x amd64/arm64
2. Code sign and notarize macOS binaries
3. Create a GitHub release with binaries and checksums
4. Update the Homebrew tap formula

**Version policy:**
- Patch bump (`0.1.x`): Bug fixes
- Minor bump (`0.x.0`): New features
- Major bump (`x.0.0`): Breaking changes

## License

MIT
