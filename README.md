# slapmac

**English** | [简体中文](./README-zh.md)

Slap your MacBook, it yells back — hit harder, hear something worse.

Uses the Apple Silicon accelerometer (Bosch BMI286 IMU via IOKit HID) to detect physical hits on your laptop and plays audio responses graded by intensity. Single binary, no dependencies.

This is a fork of [taigrr/spank](https://github.com/taigrr/spank) with a redesigned response system and a macOS menu bar app.

## Features

- **Intensity-graded responses** — no mode switching. Every slap is measured and one of three sounds plays automatically: light taps get the weak sound, harder hits escalate through medium up to strong.
- **Menu bar app** (`spank.app`) — starts spank automatically on launch, one-click stop/start, lives in your menu bar.
- **Simple packaging** — build a distributable `.app` or `.dmg` with one script.
- Playback volume follows the system output volume — no in-app volume control.
- Adaptive noise floor: a 2-second startup calibration measures ambient vibration and auto-raises the threshold in noisy environments.
- Echo filter: after each hit, detection stays silent until the chassis vibration settles, so one slap never fires twice.
- 750ms cooldown between responses to prevent rapid-fire, adjustable with `--cooldown`.
- `--stdio` JSON protocol for GUI/automation integration, and `--debug` for live sensor diagnostics.

## Requirements

- macOS on Apple Silicon (any M-series chip M2 or greater, or the M1 Pro SKU specifically, no other M1/A-series chips!)
- `sudo` (for IOKit HID accelerometer access)
- Go 1.26+ and Swift compiler (if building from source)

## Install

### Option A: DMG

Build the GUI installer yourself:

```bash
./menubar/build-dmg-gui.sh 1.0.0
# -> dist/spankbar_1.0.0_mac_arm64.dmg
```

Mount the DMG, drag `spank.app` into `Applications`, open it — spank starts automatically. On first launch the app asks once for admin rights to install the binary to `/usr/local/bin` and write a scoped sudoers entry; after that it starts silently.

> **Note:** The app is ad-hoc signed. If Gatekeeper blocks it, run `xattr -cr /Applications/spank.app` once.

### Option B: CLI only

```bash
go build -o spank .
sudo cp spank /usr/local/bin/spank
sudo spank
```

## Usage

```bash
sudo spank            # graded responses by slap intensity
sudo spank --fast     # faster polling and shorter cooldown
sudo spank --debug    # live sensor stats (samples/s, maxMag, events)

# Adjust sensitivity (default: 0.025, just above the ambient noise floor)
sudo spank --min-amplitude 0.05   # ignore very light taps

# Set cooldown period in millisecond (default: 750)
sudo spank --cooldown 600

# Set playback speed multiplier (default: 1.0)
sudo spank --speed 0.7   # slower and deeper
sudo spank --speed 1.5   # faster
```

### Intensity-graded responses

No mode switching — the response is chosen automatically by the force of  
each individual slap. Thresholds are calibrated against live M2 sensor data  
(ordinary taps register 0.02–0.06g event amplitude, hard slaps reach 0.10g+):

| Slap amplitude | Response     |
| -------------- | ------------ |
| < 0.03g        | `weak.mp3`   |
| 0.03g – 0.05g  | `medium.mp3` |
| ≥ 0.05g        | `strong.mp3` |

Swap `audio/pain/*.mp3` for your own sounds and rebuild — the filenames are the contract.

### Detection tuning

Use `--fast` for a more responsive profile with faster polling (4ms vs 10ms), shorter cooldown (350ms vs 750ms), and a larger sample batch.

`--debug` prints one line per second: sample rate, latest/max magnitude, RMS/peak, event count and the current threshold. Use it to find where your slaps land and tune the grade constants in `main.go`.

## Building

```bash
# CLI binary
go build -o spank .

# Menu bar app (dist/spank.app)
./menubar/build-app.sh 1.0.0

# Distributable DMGs
./menubar/build-dmg-gui.sh 1.0.0   # GUI app DMG
./scripts/build-dmg.sh 1.0.0       # CLI DMG (binary + install.sh)
```

## How it works

1. Reads raw accelerometer data directly via IOKit HID (Apple SPU sensor)
2. Runs vibration detection (STA/LTA, CUSUM, kurtosis, peak/MAD)
3. When a significant impact is detected, grades the amplitude and plays the matching embedded MP3
4. An echo filter keeps detection silent while the chassis is still ringing from the previous hit
5. Playback volume follows the system output volume — no in-app volume control
6. 750ms cooldown between responses to prevent rapid-fire, adjustable with `--cooldown`

## Credits

- Sensor reading and vibration detection ported from [olvvier/apple-silicon-accelerometer](https://github.com/olvvier/apple-silicon-accelerometer).
- Upstream project: [taigrr/spank](https://github.com/taigrr/spank).

## License

MIT
