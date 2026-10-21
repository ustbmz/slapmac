// spank detects slaps/hits on the laptop and plays audio responses.
// It reads the Apple Silicon accelerometer directly via IOKit HID —
// no separate sensor daemon required. Needs sudo.
//
// Responses are graded by slap intensity: light taps get the weak sound,
// harder hits escalate through medium to strong. No mode switching —
// the right sound just plays.
package main

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/charmbracelet/fang"
	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/mp3"
	"github.com/gopxl/beep/v2/speaker"
	"github.com/spf13/cobra"
	"github.com/taigrr/apple-silicon-accelerometer/detector"
	"github.com/taigrr/apple-silicon-accelerometer/sensor"
	"github.com/taigrr/apple-silicon-accelerometer/shm"
)

var version = "dev"

//go:embed audio/pain/*.mp3
var painAudio embed.FS

const painDir = "audio/pain"

// Intensity thresholds (in g) that pick which graded sound plays for a
// single slap. Calibrated from live M2 sensor data: ordinary taps register
// 0.02–0.06g event amplitude, hard slaps reach 0.10–0.15g.
const (
	thresholdMedium = 0.03 // weak   [minAmplitude, 0.03)
	thresholdStrong = 0.05 // medium [0.03, 0.05), strong >= 0.05
)

var (
	fastMode  bool
	debugMode bool
	stdioMode bool

	// Flag targets; the values are published into the atomic settings
	// below before the run starts.
	minAmplitudeFlag float64
	cooldownFlag     int
	speedFlag        float64

	// Runtime-adjustable settings. They are read every slap by the
	// detection loop and written by the stdin command handler, so they
	// must be accessed atomically.
	minAmplitude atomicFloat64
	cooldownMs   atomic.Int64 // milliseconds between audio responses
	speedRatio   atomicFloat64
	paused       atomic.Bool
)

// atomicFloat64 is a float64 backed by atomic bit ops, safe for concurrent
// use between the stdin command handler and the slap detection loop.
type atomicFloat64 struct {
	bits atomic.Uint64
}

func (a *atomicFloat64) Store(v float64) { a.bits.Store(math.Float64bits(v)) }
func (a *atomicFloat64) Load() float64   { return math.Float64frombits(a.bits.Load()) }

// sensorReady is closed once shared memory is created and the sensor
// worker is about to enter the CFRunLoop.
var sensorReady = make(chan struct{})

// sensorErr receives any error from the sensor worker.
var sensorErr = make(chan error, 1)

const (
	// defaultMinAmplitude is the default detection threshold. Ambient
	// vibration (fans, table knocks, footsteps) peaks around 0.019g on a
	// typical M2 laptop, so this sits ~30% above the measured noise floor.
	defaultMinAmplitude = 0.025

	// defaultCooldownMs is the default cooldown between audio responses.
	defaultCooldownMs = 750

	// defaultSpeedRatio is the default playback speed (1.0 = normal).
	defaultSpeedRatio = 1.0

	// defaultSensorPollInterval is how often we check for new accelerometer data.
	defaultSensorPollInterval = 10 * time.Millisecond

	// defaultMaxSampleBatch caps the number of accelerometer samples processed
	// per tick to avoid falling behind.
	defaultMaxSampleBatch = 200

	// sensorStartupDelay gives the sensor time to start producing data.
	sensorStartupDelay = 100 * time.Millisecond
)

type runtimeTuning struct {
	minAmplitude float64
	cooldown     time.Duration
	pollInterval time.Duration
	maxBatch     int
}

func defaultTuning() runtimeTuning {
	return runtimeTuning{
		minAmplitude: defaultMinAmplitude,
		cooldown:     time.Duration(defaultCooldownMs) * time.Millisecond,
		pollInterval: defaultSensorPollInterval,
		maxBatch:     defaultMaxSampleBatch,
	}
}

func applyFastOverlay(base runtimeTuning) runtimeTuning {
	base.pollInterval = 4 * time.Millisecond
	base.cooldown = 350 * time.Millisecond
	if base.minAmplitude > 0.18 {
		base.minAmplitude = 0.18
	}
	if base.maxBatch < 320 {
		base.maxBatch = 320
	}
	return base
}

// loadPainFiles returns the sorted list of embedded graded audio files.
func loadPainFiles() ([]string, error) {
	entries, err := painAudio.ReadDir(painDir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() {
			files = append(files, painDir+"/"+entry.Name())
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("no audio files found in %s", painDir)
	}
	return files, nil
}

// fileForAmplitude maps a single slap's amplitude to the graded response.
// The mapping is deterministic: hit harder, hear a more intense sound.
func fileForAmplitude(amplitude float64) string {
	switch {
	case amplitude >= thresholdStrong:
		return painDir + "/strong.mp3"
	case amplitude >= thresholdMedium:
		return painDir + "/medium.mp3"
	default:
		return painDir + "/weak.mp3"
	}
}

func main() {
	cmd := &cobra.Command{
		Use:   "spank",
		Short: "Yells back when you slap the laptop — harder hits get more intense responses",
		Long: `spank reads the Apple Silicon accelerometer directly via IOKit HID
and plays an audio response when a slap or hit is detected.

Responses are graded by intensity — light taps play the weak sound,
harder hits escalate through medium up to strong:

  amplitude < 0.03g      -> weak.mp3
  0.03g .. 0.05g         -> medium.mp3
  >= 0.05g               -> strong.mp3

Requires sudo (for IOKit HID access to the accelerometer).`,
		Version: version,
		RunE: func(cmd *cobra.Command, args []string) error {
			tuning := defaultTuning()
			if fastMode {
				tuning = applyFastOverlay(tuning)
			}
			// Explicit flags override fast preset defaults
			if cmd.Flags().Changed("min-amplitude") {
				tuning.minAmplitude = minAmplitudeFlag
			}
			if cmd.Flags().Changed("cooldown") {
				tuning.cooldown = time.Duration(cooldownFlag) * time.Millisecond
			}

			// Publish the initial tuning into the live, atomic settings so
			// the detection loop and stdin handler always read the same
			// source of truth.
			minAmplitude.Store(tuning.minAmplitude)
			cooldownMs.Store(int64(tuning.cooldown / time.Millisecond))
			speedRatio.Store(speedFlag)
			return run(cmd.Context(), tuning)
		},
		SilenceUsage: true,
	}

	cmd.Flags().BoolVar(&fastMode, "fast", false, "Enable faster detection tuning (shorter cooldown, higher sensitivity)")
	cmd.Flags().BoolVar(&debugMode, "debug", false, "Print live sensor stats once per second (for diagnosing detection)")
	cmd.Flags().Float64Var(&minAmplitudeFlag, "min-amplitude", defaultMinAmplitude, "Minimum amplitude threshold (0.0-1.0, lower = more sensitive)")
	cmd.Flags().IntVar(&cooldownFlag, "cooldown", defaultCooldownMs, "Cooldown between responses in milliseconds")
	cmd.Flags().BoolVar(&stdioMode, "stdio", false, "Enable stdio mode: JSON output and stdin commands (for GUI integration)")
	cmd.Flags().Float64Var(&speedFlag, "speed", defaultSpeedRatio, "Playback speed multiplier (0.5 = half speed, 2.0 = double speed)")

	if err := fang.Execute(context.Background(), cmd, fang.WithVersion(version)); err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context, tuning runtimeTuning) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("spank requires root privileges for accelerometer access, run with: sudo spank")
	}

	if tuning.minAmplitude < 0 || tuning.minAmplitude > 1 {
		return fmt.Errorf("--min-amplitude must be between 0.0 and 1.0")
	}
	if tuning.cooldown <= 0 {
		return fmt.Errorf("--cooldown must be greater than 0")
	}

	files, err := loadPainFiles()
	if err != nil {
		return fmt.Errorf("loading audio: %w", err)
	}
	_ = files // embedded paths are resolved by fileForAmplitude

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Create shared memory for accelerometer data.
	accelRing, err := shm.CreateRing(shm.NameAccel)
	if err != nil {
		return fmt.Errorf("creating accel shm: %w", err)
	}
	defer accelRing.Close()
	defer accelRing.Unlink()

	// Start the sensor worker in a background goroutine.
	// sensor.Run() needs runtime.LockOSThread for CFRunLoop, which it
	// handles internally. We launch detection on the current goroutine.
	go func() {
		close(sensorReady)
		if err := sensor.Run(sensor.Config{
			AccelRing: accelRing,
			Restarts:  0,
		}); err != nil {
			sensorErr <- err
		}
	}()

	// Wait for sensor to be ready.
	select {
	case <-sensorReady:
	case err := <-sensorErr:
		return fmt.Errorf("sensor worker failed: %w", err)
	case <-ctx.Done():
		return nil
	}

	// Give the sensor a moment to start producing data.
	time.Sleep(sensorStartupDelay)

	return listenForSlaps(ctx, accelRing, tuning)
}

func listenForSlaps(ctx context.Context, accelRing *shm.RingBuffer, tuning runtimeTuning) error {
	speakerInit := false
	det := detector.New()
	var lastAccelTotal uint64
	var lastEventTime time.Time
	var lastYell time.Time
	var slapCount atomic.Uint64

	// Start stdin command reader if in JSON mode
	if stdioMode {
		go readStdinCommands()
	}

	// Greet on lid open: watch the power management log for "LidOpen" wake
	// events — the signature written when the lid is opened from sleep. While
	// the lid is shut the Mac usually sleeps; on wake the poll resumes and sees
	// the new entry, so the greeting fires shortly after the lid opens.
	go watchLidOpen(ctx, func() {
		fmt.Println("spank: lid opened")
		go playAudio(painDir+"/medium.mp3", &speakerInit)
	})

	presetLabel := "default"
	if fastMode {
		presetLabel = "fast"
	}
	fmt.Printf("spank: listening for slaps in %s tuning... (harder hits get more intense responses; ctrl+c to quit)\n", presetLabel)
	if stdioMode {
		fmt.Println(`{"status":"ready"}`)
	}

	ticker := time.NewTicker(tuning.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nbye!")
			return nil
		case err := <-sensorErr:
			return fmt.Errorf("sensor worker failed: %w", err)
		case <-ticker.C:
		}

		// Check if paused
		if paused.Load() {
			continue
		}

		now := time.Now()
		tNow := float64(now.UnixNano()) / 1e9

		samples, newTotal := accelRing.ReadNew(lastAccelTotal, shm.AccelScale)
		lastAccelTotal = newTotal
		if len(samples) > tuning.maxBatch {
			samples = samples[len(samples)-tuning.maxBatch:]
		}

		nSamples := len(samples)
		for idx, sample := range samples {
			tSample := tNow - float64(nSamples-idx-1)/float64(det.FS)
			det.Process(sample.X, sample.Y, sample.Z, tSample)
		}

		// Debug mode: print live sensor stats once per second so users can
		// tell whether data flows at all and how large their taps register
		// compared to the amplitude thresholds.
		if debugMode {
			dbgSamples += nSamples
			if m := det.LatestMag; m > dbgMaxMag {
				dbgMaxMag = m
			}
			dbgEvents = len(det.Events)
			if now.Sub(lastDbgPrint) >= time.Second {
				lastDbgPrint = now
				fmt.Printf("debug: samples/s=%d latestMag=%.5fg maxMag=%.5fg rms=%.5f peak=%.5f events=%d threshold=%.3f\n",
					dbgSamples, det.LatestMag, dbgMaxMag, det.RMS, det.Peak, dbgEvents, minAmplitude.Load())
				dbgSamples = 0
				dbgMaxMag = 0
			}
		}

		if len(det.Events) == 0 {
			continue
		}

		ev := det.Events[len(det.Events)-1]
		if ev.Time.Equal(lastEventTime) {
			continue
		}
		lastEventTime = ev.Time

		// Read live settings each event so stdin "set" takes effect
		// immediately and the fast-preset tuning snapshot is honoured.
		cooldown := time.Duration(cooldownMs.Load()) * time.Millisecond
		if time.Since(lastYell) <= cooldown {
			continue
		}
		if ev.Amplitude < minAmplitude.Load() {
			continue
		}

		lastYell = now
		num := slapCount.Add(1)
		file := fileForAmplitude(ev.Amplitude)
		if stdioMode {
			event := map[string]interface{}{
				"timestamp":  now.Format(time.RFC3339Nano),
				"slapNumber": num,
				"amplitude":  ev.Amplitude,
				"severity":   string(ev.Severity),
				"file":       file,
			}
			if data, err := json.Marshal(event); err == nil {
				fmt.Println(string(data))
			}
		} else {
			fmt.Printf("slap #%d [%s amp=%.5fg] -> %s\n", num, ev.Severity, ev.Amplitude, file)
		}
		go playAudio(file, &speakerInit)
	}
}

// Debug-mode accumulators (printed once per second).
var (
	dbgSamples   int
	dbgMaxMag    float64
	dbgEvents    int
	lastDbgPrint time.Time
)

var speakerMu sync.Mutex

// speakerSampleRate is the sample rate the speaker was initialized with.
// The speaker is initialized exactly once, using the rate of the first
// file played. Every subsequent file must be resampled to this rate or it
// will play at the wrong speed/pitch.
var speakerSampleRate beep.SampleRate

// resampleForPlayback wraps source so it is delivered at speakerRate, folding
// in an optional playback speed change. The speed change reuses the same
// resample by claiming the source rate is fileRate*speed.
func resampleForPlayback(source beep.Streamer, fileRate, speakerRate beep.SampleRate, speed float64) beep.Streamer {
	srcRate := fileRate
	if speed != 1.0 && speed > 0 {
		srcRate = beep.SampleRate(int(float64(fileRate) * speed))
	}
	if srcRate == speakerRate {
		return source
	}
	return beep.Resample(4, srcRate, speakerRate, source)
}

// watchLidOpen invokes onOpen whenever the laptop lid is opened. It polls
// `pmset -g log` (readable without special privileges) for new wake entries
// whose signature contains "LidOpen". lastStamp starts at "now" so historical
// entries never fire; only entries newer than startup count.
func watchLidOpen(ctx context.Context, onOpen func()) {
	const stampLen = len("2006-01-02 15:04:05")
	lastStamp := time.Now().Format("2006-01-02 15:04:05")
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
		out, err := exec.Command("pmset", "-g", "log").Output()
		if err != nil {
			continue
		}
		maxSeen := lastStamp
		for _, line := range strings.Split(string(out), "\n") {
			if len(line) < stampLen {
				continue
			}
			stamp := line[:stampLen]
			if stamp > maxSeen {
				maxSeen = stamp
			}
			if stamp <= lastStamp {
				continue
			}
			// Match the exact wake signature "LidOpen" (e.g. "due to
			// EC.LidOpen(E)"). A bare "lid" substring would also match
			// common words like "valid"/"invalid" in unrelated pmset
			// lines and fire phantom greetings.
			if strings.Contains(line, "Wake") && strings.Contains(strings.ToLower(line), "lidopen") {
				onOpen()
			}
		}
		lastStamp = maxSeen
	}
}

func playAudio(path string, speakerInit *bool) {
	data, err := painAudio.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spank: read %s: %v\n", path, err)
		return
	}
	streamer, format, err := mp3.Decode(io.NopCloser(bytes.NewReader(data)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "spank: decode %s: %v\n", path, err)
		return
	}
	defer streamer.Close()

	speakerMu.Lock()
	if !*speakerInit {
		speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/10))
		speakerSampleRate = format.SampleRate
		*speakerInit = true
	}
	targetRate := speakerSampleRate
	speakerMu.Unlock()

	// Playback volume simply follows the system output volume.

	// Resample to the speaker's rate. The speaker is initialized once with
	// the first file's rate; any file with a different rate would otherwise
	// play at the wrong speed/pitch. The --speed flag is folded into the same
	// resample by claiming the source is at fileRate*speed.
	source := resampleForPlayback(streamer, format.SampleRate, targetRate, speedRatio.Load())

	done := make(chan bool)
	speaker.Play(beep.Seq(source, beep.Callback(func() {
		done <- true
	})))
	<-done
}

// stdinCommand represents a command received via stdin
type stdinCommand struct {
	Cmd       string  `json:"cmd"`
	Amplitude float64 `json:"amplitude,omitempty"`
	Cooldown  int     `json:"cooldown,omitempty"`
	Speed     float64 `json:"speed,omitempty"`
}

// readStdinCommands reads JSON commands from stdin for live control
func readStdinCommands() {
	processCommands(os.Stdin, os.Stdout)
}

// processCommands reads JSON commands from r and writes responses to w.
// This is the testable core of the stdin command handler.
func processCommands(r io.Reader, w io.Writer) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var cmd stdinCommand
		if err := json.Unmarshal([]byte(line), &cmd); err != nil {
			if stdioMode {
				fmt.Fprintf(w, `{"error":"invalid command: %s"}%s`, err.Error(), "\n")
			}
			continue
		}

		switch cmd.Cmd {
		case "pause":
			paused.Store(true)
			if stdioMode {
				fmt.Fprintln(w, `{"status":"paused"}`)
			}
		case "resume":
			paused.Store(false)
			if stdioMode {
				fmt.Fprintln(w, `{"status":"resumed"}`)
			}
		case "set":
			if cmd.Amplitude > 0 && cmd.Amplitude <= 1 {
				minAmplitude.Store(cmd.Amplitude)
			}
			if cmd.Cooldown > 0 {
				cooldownMs.Store(int64(cmd.Cooldown))
			}
			if cmd.Speed > 0 {
				speedRatio.Store(cmd.Speed)
			}
			if stdioMode {
				fmt.Fprintf(w, `{"status":"settings_updated","amplitude":%.4f,"cooldown":%d,"speed":%.2f}%s`, minAmplitude.Load(), cooldownMs.Load(), speedRatio.Load(), "\n")
			}
		case "status":
			if stdioMode {
				fmt.Fprintf(w, `{"status":"ok","paused":%t,"amplitude":%.4f,"cooldown":%d,"speed":%.2f}%s`, paused.Load(), minAmplitude.Load(), cooldownMs.Load(), speedRatio.Load(), "\n")
			}
		case "quit":
			// Graceful shutdown for GUI wrappers: spank runs as root, so a
			// non-root parent cannot kill it. Self-delivered SIGINT bypasses
			// that restriction and hits the same clean shutdown path as ^C.
			if stdioMode {
				fmt.Fprintln(w, `{"status":"quitting"}`)
			}
			go func() {
				time.Sleep(100 * time.Millisecond) // let the response flush
				syscall.Kill(os.Getpid(), syscall.SIGINT)
			}()
		default:
			if stdioMode {
				fmt.Fprintf(w, `{"error":"unknown command: %s"}%s`, cmd.Cmd, "\n")
			}
		}
	}
}
