# AGENTS.md

> Guidelines for AI agents working in this repository.

## Project Overview

**spank** is a macOS CLI tool that detects physical hits/slaps on Apple Silicon MacBooks via the accelerometer and plays intensity-graded audio responses. Single-file Go application with embedded MP3 assets.

- **Platform**: macOS on Apple Silicon (M2+) only
- **Runtime requirement**: `sudo` (for IOKit HID accelerometer access)
- **Architecture**: Single `main.go` file with embedded audio assets

## Commands

### Build & Run

```bash
# Build
go build -o spank .

# Run (requires sudo)
sudo ./spank

# Diagnose detection (live sensor stats)
sudo ./spank --debug
```

> Note: this fork adds `menubar/` (Swift menu bar GUI) and `scripts/build-dmg.sh` /
> `menubar/build-app.sh` / `menubar/build-dmg-gui.sh` packaging scripts. Use
> `GOPROXY=https://goproxy.cn,direct` if the default Go proxy is unreachable.

### Install

```bash
go install github.com/taigrr/spank@latest
```

### Release

Releases are automated via GitHub Actions + GoReleaser Pro when a `v*` tag is pushed:

```bash
git tag v1.0.0
git push origin v1.0.0
```

## Code Organization

```
spank/
├── main.go              # All application code (single file)
├── audio/
│   └── pain/            # 4 intensity-graded responses:
│                        #   weak / medium / strong / brutal (MP3)
├── menubar/             # Swift menu bar GUI (spankbar) + build scripts
├── scripts/             # CLI DMG packaging
├── go.mod
├── .goreleaser.yaml     # Release configuration
└── .github/workflows/   # CI/CD
```

## Key Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/taigrr/apple-silicon-accelerometer` | Reads accelerometer via IOKit HID |
| `github.com/gopxl/beep/v2` | Audio playback (MP3 decoding, speaker output) |
| `github.com/spf13/cobra` | CLI framework |
| `github.com/charmbracelet/fang` | CLI config/execution wrapper (version must be passed via `fang.WithVersion`) |

## Code Patterns

### Embedded Assets

Audio files are embedded at compile time using `//go:embed`:

```go
//go:embed audio/pain/*.mp3
var painAudio embed.FS
```

### Intensity-graded selection

There are no user-selectable modes. `fileForAmplitude(amplitude)` maps the
detected amplitude of each individual slap to one of the four graded sounds:

| amplitude   | file          |
|-------------|---------------|
| < 0.15g     | `weak.mp3`    |
| 0.15–0.35g  | `medium.mp3`  |
| 0.35–0.60g  | `strong.mp3`  |
| ≥ 0.60g     | `brutal.mp3`  |

Thresholds are the `thresholdMedium/Strong/Brutal` constants in `main.go`.

### Runtime settings

`minAmplitude`, `cooldownMs`, `speedRatio` and `paused` are `sync/atomic`
values shared between the detection loop and the stdin command handler —
never replace them with plain globals.

### Slap Detection Flow

1. `sensor.Run()` reads accelerometer in background goroutine
2. Data shared via `shm.RingBuffer` (POSIX shared memory)
3. `detector.New()` processes samples with vibration detection algorithms
4. Events trigger audio playback with 750ms cooldown

### Concurrency

- `speakerMu sync.Mutex` protects speaker initialization
- Audio playback runs in goroutines (`go playAudio(...)`)
- `slapCount atomic.Uint64` counts slaps

## Constants

Key tuning parameters in `main.go`:

| Constant | Value | Purpose |
|----------|-------|---------|
| `defaultCooldownMs` | 750ms | Minimum time between audio plays |
| `defaultSensorPollInterval` | 10ms | Accelerometer polling rate |
| `defaultMaxSampleBatch` | 200 | Max samples processed per tick |
| `thresholdMedium/Strong/Brutal` | 0.15/0.35/0.60 g | Intensity grading |

## Gotchas

1. **Root required**: The app must run with `sudo` for IOKit HID access. The `run()` function checks `os.Geteuid() != 0`.
2. **Apple Silicon only**: Only builds for `darwin/arm64`. Intel Macs are not supported.
3. **Private dependency**: `github.com/taigrr/apple-silicon-accelerometer` requires `GOPRIVATE` setting and GitHub PAT for CI.
4. **Single file**: All Go code is in `main.go`. When adding features, follow the existing pattern of types and functions in the same file.
5. **CGO disabled**: Builds use `CGO_ENABLED=0` despite targeting macOS.
6. **stdin `quit` command**: spank runs as root so a non-root GUI cannot kill it; the stdio `quit` command self-delivers SIGINT. Keep it when refactoring `processCommands`.

## Adding Audio

To change the graded sounds:

1. Replace MP3s in `audio/pain/` (keep the `weak/medium/strong/brutal` names,
   or update `fileForAmplitude` accordingly)
2. Rebuild — files are embedded at compile time

## Version

Version is injected via ldflags at build time:

```go
var version = "dev"
```

GoReleaser and the local packaging scripts set `-X main.version={{.Version}}` during builds.
