// Package watchdog provides Linux hardware watchdog support for split-brain prevention.
package watchdog

import (
	"context"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/rs/zerolog/log"
)

// Watchdog modes
const (
	ModeOff      = "off"
	ModeAutomatic = "automatic"
	ModeRequired = "required"
)

// Linux watchdog ioctl commands
const (
	WDIOC_GETSUPPORT    = 0x80285700
	WDIOC_GETSTATUS     = 0x80045701
	WDIOC_GETBOOTSTATUS = 0x80045702
	WDIOC_GETTEMP       = 0x80045703
	WDIOC_SETOPTIONS    = 0x80045704
	WDIOC_KEEPALIVE     = 0x80045705
	WDIOC_SETTIMEOUT    = 0xc0045706
	WDIOC_GETTIMEOUT    = 0x80045707
	WDIOC_SETPRETIMEOUT = 0xc0045708
	WDIOC_GETPRETIMEOUT = 0x80045709
	WDIOC_GETTIMELEFT   = 0x8004570a

	WDIOS_DISABLECARD   = 0x0001
	WDIOS_ENABLECARD    = 0x0002
)

// WatchdogInfo represents the watchdog device info.
type WatchdogInfo struct {
	Options         uint32
	FirmwareVersion uint32
	Identity        [32]byte
}

// Watchdog manages the Linux hardware watchdog device.
type Watchdog struct {
	mu            sync.Mutex
	device        string
	mode          string
	file          *os.File
	timeout       time.Duration
	safetyMargin  time.Duration
	isActive      bool
	cancel        context.CancelFunc
}

// Config holds watchdog configuration.
type Config struct {
	Mode         string        `yaml:"mode" json:"mode"`
	Device       string        `yaml:"device" json:"device"`
	SafetyMargin time.Duration `yaml:"safety_margin" json:"safety_margin"`
}

// DefaultConfig returns the default watchdog configuration.
func DefaultConfig() *Config {
	return &Config{
		Mode:         ModeAutomatic,
		Device:       "/dev/watchdog",
		SafetyMargin: 5 * time.Second,
	}
}

// New creates a new Watchdog instance.
func New(config *Config) (*Watchdog, error) {
	if config == nil {
		config = DefaultConfig()
	}

	if config.Device == "" {
		config.Device = "/dev/watchdog"
	}

	if config.SafetyMargin == 0 {
		config.SafetyMargin = 5 * time.Second
	}

	w := &Watchdog{
		device:       config.Device,
		mode:         config.Mode,
		safetyMargin: config.SafetyMargin,
	}

	return w, nil
}

// Open opens and activates the watchdog device.
func (w *Watchdog) Open() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.mode == ModeOff {
		log.Info().Msg("Watchdog is disabled by configuration")
		return nil
	}

	// Try to open the watchdog device
	file, err := os.OpenFile(w.device, os.O_RDWR, 0)
	if err != nil {
		if w.mode == ModeRequired {
			return fmt.Errorf("watchdog is required but device %s not available: %w", w.device, err)
		}
		log.Warn().Err(err).Str("device", w.device).Msg("Watchdog device not available")
		return nil
	}

	w.file = file
	w.isActive = true

	// Get the current timeout
	timeout, err := w.getTimeout()
	if err != nil {
		w.file.Close()
		w.file = nil
		w.isActive = false
		return fmt.Errorf("failed to get watchdog timeout: %w", err)
	}
	w.timeout = timeout

	log.Info().
		Str("device", w.device).
		Dur("timeout", w.timeout).
		Msg("Watchdog device opened")

	return nil
}

// Close closes the watchdog device.
func (w *Watchdog) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.cancel != nil {
		w.cancel()
	}

	if w.file != nil {
		// Write 'V' to disable the watchdog on close (magic close)
		w.file.Write([]byte("V"))
		err := w.file.Close()
		w.file = nil
		w.isActive = false
		return err
	}

	return nil
}

// IsActive returns true if the watchdog is currently active.
func (w *Watchdog) IsActive() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.isActive
}

// Keepalive sends a keepalive signal to the watchdog.
func (w *Watchdog) Keepalive() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		w.file.Fd(),
		WDIOC_KEEPALIVE,
		0)
	if errno != 0 {
		return fmt.Errorf("ioctl WDIOC_KEEPALIVE failed: %v", errno)
	}

	log.Debug().Msg("Watchdog keepalive sent")
	return nil
}

// SetTimeout sets the watchdog timeout.
func (w *Watchdog) SetTimeout(timeout time.Duration) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return fmt.Errorf("watchdog not open")
	}

	seconds := int32(timeout.Seconds())
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		w.file.Fd(),
		WDIOC_SETTIMEOUT,
		uintptr(unsafe.Pointer(&seconds)))
	if errno != 0 {
		return fmt.Errorf("ioctl WDIOC_SETTIMEOUT failed: %v", errno)
	}

	w.timeout = time.Duration(seconds) * time.Second
	log.Info().Dur("timeout", w.timeout).Msg("Watchdog timeout set")
	return nil
}

// getTimeout gets the current watchdog timeout.
func (w *Watchdog) getTimeout() (time.Duration, error) {
	var seconds int32
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		w.file.Fd(),
		WDIOC_GETTIMEOUT,
		uintptr(unsafe.Pointer(&seconds)))
	if errno != 0 {
		return 0, fmt.Errorf("ioctl WDIOC_GETTIMEOUT failed: %v", errno)
	}

	return time.Duration(seconds) * time.Second, nil
}

// GetTimeout returns the current watchdog timeout.
func (w *Watchdog) GetTimeout() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.timeout
}

// GetTimeLeft returns the time left before the watchdog fires.
func (w *Watchdog) GetTimeLeft() (time.Duration, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return 0, fmt.Errorf("watchdog not open")
	}

	var seconds int32
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		w.file.Fd(),
		WDIOC_GETTIMELEFT,
		uintptr(unsafe.Pointer(&seconds)))
	if errno != 0 {
		return 0, fmt.Errorf("ioctl WDIOC_GETTIMELEFT failed: %v", errno)
	}

	return time.Duration(seconds) * time.Second, nil
}

// GetInfo returns information about the watchdog device.
func (w *Watchdog) GetInfo() (*WatchdogInfo, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil, fmt.Errorf("watchdog not open")
	}

	var info WatchdogInfo
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		w.file.Fd(),
		WDIOC_GETSUPPORT,
		uintptr(unsafe.Pointer(&info)))
	if errno != 0 {
		return nil, fmt.Errorf("ioctl WDIOC_GETSUPPORT failed: %v", errno)
	}

	return &info, nil
}

// Disable disables the watchdog (if supported).
func (w *Watchdog) Disable() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}

	options := int32(WDIOS_DISABLECARD)
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		w.file.Fd(),
		WDIOC_SETOPTIONS,
		uintptr(unsafe.Pointer(&options)))
	if errno != 0 {
		return fmt.Errorf("ioctl WDIOC_SETOPTIONS (disable) failed: %v", errno)
	}

	w.isActive = false
	log.Info().Msg("Watchdog disabled")
	return nil
}

// Enable enables the watchdog (if supported).
func (w *Watchdog) Enable() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return fmt.Errorf("watchdog not open")
	}

	options := int32(WDIOS_ENABLECARD)
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		w.file.Fd(),
		WDIOC_SETOPTIONS,
		uintptr(unsafe.Pointer(&options)))
	if errno != 0 {
		return fmt.Errorf("ioctl WDIOC_SETOPTIONS (enable) failed: %v", errno)
	}

	w.isActive = true
	log.Info().Msg("Watchdog enabled")
	return nil
}

// StartKeepalive starts a background goroutine that sends keepalives.
func (w *Watchdog) StartKeepalive(ctx context.Context, interval time.Duration) {
	ctx, cancel := context.WithCancel(ctx)
	w.mu.Lock()
	w.cancel = cancel
	w.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := w.Keepalive(); err != nil {
					log.Error().Err(err).Msg("Failed to send watchdog keepalive")
				}
			}
		}
	}()

	log.Info().Dur("interval", interval).Msg("Started watchdog keepalive goroutine")
}

// CalculateKeepaliveInterval calculates the optimal keepalive interval.
func (w *Watchdog) CalculateKeepaliveInterval(loopWait time.Duration) time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.timeout == 0 {
		return loopWait
	}

	// Keepalive should be at least safety_margin seconds before timeout
	maxInterval := w.timeout - w.safetyMargin
	if maxInterval < time.Second {
		maxInterval = time.Second
	}

	// Use the smaller of loopWait and maxInterval
	if loopWait < maxInterval {
		return loopWait
	}
	return maxInterval
}

// Mode returns the current watchdog mode.
func (w *Watchdog) Mode() string {
	return w.mode
}

// IsAvailable checks if a watchdog device is available without opening it.
func IsAvailable(device string) bool {
	if device == "" {
		device = "/dev/watchdog"
	}

	_, err := os.Stat(device)
	return err == nil
}

// SoftdogLoader loads the softdog kernel module.
type SoftdogLoader struct{}

// NewSoftdogLoader creates a new SoftdogLoader.
func NewSoftdogLoader() *SoftdogLoader {
	return &SoftdogLoader{}
}

// Load loads the softdog module with the specified timeout.
func (l *SoftdogLoader) Load(timeout int) error {
	// This would typically use syscall.Syscall to call init_module
	// or exec modprobe. For now, we log the intent.
	log.Info().Int("timeout", timeout).Msg("Would load softdog module")
	return nil
}

// WatchdogMonitor provides high-level watchdog management for Patroni.
type WatchdogMonitor struct {
	watchdog *Watchdog
	isLeader bool
	mu       sync.Mutex
}

// NewWatchdogMonitor creates a new WatchdogMonitor.
func NewWatchdogMonitor(config *Config) (*WatchdogMonitor, error) {
	w, err := New(config)
	if err != nil {
		return nil, err
	}

	return &WatchdogMonitor{
		watchdog: w,
	}, nil
}

// Open opens the watchdog device.
func (m *WatchdogMonitor) Open() error {
	return m.watchdog.Open()
}

// Close closes the watchdog device.
func (m *WatchdogMonitor) Close() error {
	return m.watchdog.Close()
}

// SetLeaderState updates the leader state and manages watchdog accordingly.
func (m *WatchdogMonitor) SetLeaderState(isLeader bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.isLeader == isLeader {
		return
	}

	m.isLeader = isLeader

	if isLeader {
		// Becoming leader - activate watchdog
		if m.watchdog.IsActive() {
			m.watchdog.Keepalive()
			log.Info().Msg("Watchdog activated for leader role")
		}
	} else {
		// Lost leadership - disable watchdog to prevent reboot
		if m.watchdog.IsActive() {
			m.watchdog.Disable()
			log.Info().Msg("Watchdog disabled after losing leadership")
		}
	}
}

// Keepalive sends a keepalive if we're the leader.
func (m *WatchdogMonitor) Keepalive() error {
	m.mu.Lock()
	isLeader := m.isLeader
	m.mu.Unlock()

	if !isLeader {
		return nil
	}

	return m.watchdog.Keepalive()
}

// IsActive returns true if the watchdog is active.
func (m *WatchdogMonitor) IsActive() bool {
	return m.watchdog.IsActive()
}
