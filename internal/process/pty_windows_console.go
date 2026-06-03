//go:build windows

package process

import (
	"fmt"
	"hash/crc32"
	"log"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// ── Console API types ────────────────────────────────────────────
// (_COORD is shared with pty_windows.go)

type _SMALL_RECT struct{ Left, Top, Right, Bottom int16 }
type _CONSOLE_SCREEN_BUFFER_INFO struct {
	Size           _COORD
	CursorPosition _COORD
	Attributes     uint16
	Window         _SMALL_RECT
	MaxWindowSize  _COORD
}
type _CHAR_INFO struct {
	Char       uint16
	Attributes uint16
}
type _KEY_EVENT_RECORD struct {
	KeyDown         int32
	RepeatCount     uint16
	VirtualKeyCode  uint16
	VirtualScanCode uint16
	UnicodeChar     uint16
	ControlKeyState uint32
}
type _INPUT_RECORD struct {
	EventType uint16
	_         [2]byte
	Event     _KEY_EVENT_RECORD
}

const (
	_SW_HIDE            = 0
	_KEY_EVENT          = 0x0001
	_CREATE_NEW_CONSOLE = 0x00000010
)

// ── Lazy DLL procs for console APIs ──────────────────────────────

var (
	modKernel32Console = syscall.NewLazyDLL("kernel32.dll")
	modUser32Console   = syscall.NewLazyDLL("user32.dll")

	procFreeConsoleConsole                = modKernel32Console.NewProc("FreeConsole")
	procAttachConsoleConsole              = modKernel32Console.NewProc("AttachConsole")
	procGetConsoleWindowConsole           = modKernel32Console.NewProc("GetConsoleWindow")
	procShowWindowConsole                 = modUser32Console.NewProc("ShowWindow")
	procSetWindowPosConsole               = modUser32Console.NewProc("SetWindowPos")
	procGetConsoleScreenBufferInfoConsole = modKernel32Console.NewProc("GetConsoleScreenBufferInfo")
	procReadConsoleOutputConsole          = modKernel32Console.NewProc("ReadConsoleOutputW")
	procWriteConsoleInputConsole          = modKernel32Console.NewProc("WriteConsoleInputW")
	procSetConsoleScreenBufferSizeConsole = modKernel32Console.NewProc("SetConsoleScreenBufferSize")
	procSetConsoleWindowInfoConsole       = modKernel32Console.NewProc("SetConsoleWindowInfo")
	procWaitForSingleObjectConsole        = modKernel32Console.NewProc("WaitForSingleObject")
	procCloseHandleConsole                = modKernel32Console.NewProc("CloseHandle")
	procTerminateProcessConsole           = modKernel32Console.NewProc("TerminateProcess")
	procGetExitCodeProcessConsole         = modKernel32Console.NewProc("GetExitCodeProcess")
	procCreateProcessWConsole             = modKernel32Console.NewProc("CreateProcessW")
	procResumeThreadConsole               = modKernel32Console.NewProc("ResumeThread")
)

// ── consoleDriver ─────────────────────────────────────────────────

// consoleDriver implements PTYDriver for Windows using a real (hidden) console
// with screen-buffer scraping. This is the fallback for systems where ConPTY
// is not available or broken (e.g. Insider builds).
type consoleDriver struct {
	processHandle syscall.Handle
	processID     uint32

	// Console I/O handles — valid only while attached to the child's console.
	conOut syscall.Handle
	conIn  syscall.Handle

	cols, rows int

	// Polling control
	stopCh chan struct{}
	mu     sync.Mutex

	// Full-screen change detection for TUI support.
	// We read the entire visible buffer each poll and compare against
	// the last-rendered checksum. Only push when content actually changed.
	lastChecksum uint32
}

func (d *consoleDriver) Write(data string) error {
	consoleAttachMu.Lock()
	attachToConsole(d.processID)
	consoleAttachMu.Unlock()

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.conIn == 0 {
		return fmt.Errorf("console input handle not available")
	}

	for _, ch := range data {
		rec := _INPUT_RECORD{EventType: _KEY_EVENT}
		rec.Event.KeyDown = 1
		rec.Event.UnicodeChar = uint16(ch)
		rec.Event.VirtualKeyCode = charToVK(ch)

		if ch == '\r' {
			rec.Event.VirtualKeyCode = 0x0D // VK_RETURN
			rec.Event.UnicodeChar = 0x0D
		}

		recUp := rec
		recUp.Event.KeyDown = 0

		var written uint32
		procWriteConsoleInputConsole.Call(
			uintptr(d.conIn), uintptr(unsafe.Pointer(&rec)), 1, uintptr(unsafe.Pointer(&written)),
		)
		procWriteConsoleInputConsole.Call(
			uintptr(d.conIn), uintptr(unsafe.Pointer(&recUp)), 1, uintptr(unsafe.Pointer(&written)),
		)
	}
	return nil
}

func (d *consoleDriver) Resize(cols, rows int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.conOut == 0 {
		return fmt.Errorf("console output handle not available")
	}

	d.cols = cols
	d.rows = rows

	bufSize := _COORD{X: int16(cols), Y: int16(rows * 10)} // tall scrollback
	procSetConsoleScreenBufferSizeConsole.Call(
		uintptr(d.conOut), uintptr(*(*uint32)(unsafe.Pointer(&bufSize))),
	)

	// Set window size (visible area)
	winRect := _SMALL_RECT{Left: 0, Top: 0, Right: int16(cols) - 1, Bottom: int16(rows) - 1}
	procSetConsoleWindowInfoConsole.Call(
		uintptr(d.conOut), 1, uintptr(unsafe.Pointer(&winRect)),
	)

	return nil
}

func (d *consoleDriver) Close() error {
	if d.stopCh != nil {
		close(d.stopCh)
		d.stopCh = nil
	}
	if d.conOut != 0 {
		syscall.CloseHandle(d.conOut)
		d.conOut = 0
	}
	if d.conIn != 0 {
		syscall.CloseHandle(d.conIn)
		d.conIn = 0
	}
	// Detach from the child's console so we're not left dangling.
	procFreeConsoleConsole.Call()
	return nil
}

func (d *consoleDriver) PtyMode() string { return "console" }

// Global lock for console attachment operations.
// Windows allows only one console attachment per process. When multiple
// console-mode sessions exist, we must re-attach before each I/O call.
var consoleAttachMu sync.Mutex

// attachToConsole detaches from any existing console and attaches to the
// specified process's console. Must be called before any console I/O.
func attachToConsole(pid uint32) error {
	procFreeConsoleConsole.Call()
	r, _, errno := procAttachConsoleConsole.Call(uintptr(pid))
	if r == 0 {
		return fmt.Errorf("AttachConsole(%d): errno=%d", pid, errno)
	}
	return nil
}

// readScreen reads the entire visible console screen buffer each poll, hashes
// the raw content with CRC32, and only emits output when the screen changed.
// Full-screen ANSI clear (\x1b[2J\x1b[H) is prepended so terminal emulators
// redraw correctly — this is required for TUI applications.
func (d *consoleDriver) readScreen() string {
	consoleAttachMu.Lock()
	defer consoleAttachMu.Unlock()

	if err := attachToConsole(d.processID); err != nil {
		log.Printf("[console] readScreen attachToConsole(%d): %v", d.processID, err)
		return ""
	}

	d.mu.Lock()
	rows := d.rows
	cols := d.cols
	conOut := d.conOut
	d.mu.Unlock()

	if conOut == 0 {
		log.Printf("[console] readScreen: conOut is 0 for PID %d", d.processID)
		return ""
	}

	var csbi _CONSOLE_SCREEN_BUFFER_INFO
	r, _, _ := procGetConsoleScreenBufferInfoConsole.Call(
		uintptr(conOut), uintptr(unsafe.Pointer(&csbi)),
	)
	if r == 0 {
		log.Printf("[console] readScreen GetConsoleScreenBufferInfo failed for PID %d", d.processID)
		return ""
	}

	readCols := int(csbi.Size.X)
	if readCols > cols {
		readCols = cols
	}
	if readCols <= 0 {
		readCols = 80
	}

	// Read the entire visible window — full-screen for TUI support.
	readRegion := _SMALL_RECT{
		Left:   0,
		Top:    0,
		Right:  int16(readCols) - 1,
		Bottom: int16(rows) - 1,
	}
	bufCoord := _COORD{X: int16(readCols), Y: int16(rows)}
	buf := make([]_CHAR_INFO, readCols*rows)

	r, _, _ = procReadConsoleOutputConsole.Call(
		uintptr(conOut),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(*(*uint32)(unsafe.Pointer(&bufCoord))),
		uintptr(*(*uint32)(unsafe.Pointer(&_COORD{X: 0, Y: 0}))),
		uintptr(unsafe.Pointer(&readRegion)),
	)
	if r == 0 {
		return ""
	}

	// Hash raw buffer to detect changes. Only push when content actually changed.
	h := crc32.NewIEEE()
	bufBytes := unsafe.Slice((*byte)(unsafe.Pointer(&buf[0])), len(buf)*4)
	h.Write(bufBytes)
	sum := h.Sum32()

	d.mu.Lock()
	if sum == d.lastChecksum {
		d.mu.Unlock()
		return ""
	}
	d.lastChecksum = sum
	d.mu.Unlock()

	// Build full-screen output with ANSI clear for terminal emulator redraw.
	var lines []string
	for row := 0; row < rows; row++ {
		line := make([]byte, 0, readCols)
		for col := 0; col < readCols; col++ {
			ch := buf[row*readCols+col].Char
			if ch == 0 {
				break
			}
			if ch < 128 {
				line = append(line, byte(ch))
			} else {
				encoded := []byte(string(rune(ch)))
				line = append(line, encoded...)
			}
		}
		for len(line) > 0 && line[len(line)-1] == ' ' {
			line = line[:len(line)-1]
		}
		lines = append(lines, string(line))
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	result := "\x1b[2J\x1b[H"
	for i, line := range lines {
		result += line
		if i < len(lines)-1 {
			result += "\r\n"
		}
	}
	return result
}

// ── Console process creation ──────────────────────────────────────

// createConsoleProcess spawns a process with a real (hidden) console.
// Returns the driver and the process info needed for cleanup and waiting.
func createConsoleProcess(command string, args []string, cwd string, cols, rows int) (*consoleDriver, *_PROCESS_INFORMATION, error) {
	// Build command line
	cmdLine := command
	if command == "cmd" && len(args) == 0 {
		cmdLine = "cmd /K"
	}
	for _, a := range args {
		cmdLine += " " + a
	}
	cmdLinePtr, err := syscall.UTF16PtrFromString(cmdLine)
	if err != nil {
		return nil, nil, fmt.Errorf("utf16 command line: %w", err)
	}

	var cwdPtr *uint16
	if cwd != "" {
		cwdPtr, err = syscall.UTF16PtrFromString(cwd)
		if err != nil {
			return nil, nil, fmt.Errorf("utf16 cwd: %w", err)
		}
	}

	// CREATE_NEW_CONSOLE ensures the child gets its own console regardless
	// of our current attachment. No FreeConsole needed before CreateProcess.

	si := &syscall.StartupInfo{}
	si.Cb = uint32(unsafe.Sizeof(*si))
	si.ShowWindow = _SW_HIDE
	si.Flags = 0x00000001 // STARTF_USESHOWWINDOW

	pi := &_PROCESS_INFORMATION{}

	r, _, err := procCreateProcessWConsole.Call(
		0, uintptr(unsafe.Pointer(cmdLinePtr)),
		0, 0, 0, _CREATE_NEW_CONSOLE,
		0, uintptr(unsafe.Pointer(cwdPtr)),
		uintptr(unsafe.Pointer(si)), uintptr(unsafe.Pointer(pi)),
	)
	if r == 0 {
		return nil, nil, fmt.Errorf("CreateProcessW: %w", err)
	}

	// Give the console a moment to initialize
	time.Sleep(100 * time.Millisecond)

	// Atomically detach from any current console, attach to the child's,
	// and open I/O handles. This must be serialised across all sessions
	// because Windows allows only one console attachment per process.
	consoleAttachMu.Lock()
	defer consoleAttachMu.Unlock()

	procFreeConsoleConsole.Call()

	r, _, errno2 := procAttachConsoleConsole.Call(uintptr(pi.ProcessID))
	if r == 0 {
		syscall.CloseHandle(syscall.Handle(pi.Thread))
		syscall.CloseHandle(syscall.Handle(pi.Process))
		return nil, nil, fmt.Errorf("AttachConsole(%d): errno=%d", pi.ProcessID, errno2)
	}

	// Hide the console window
	if hwnd, _, _ := procGetConsoleWindowConsole.Call(); hwnd != 0 {
		procShowWindowConsole.Call(hwnd, _SW_HIDE)
		offScreen := int32(-32000)
		procSetWindowPosConsole.Call(hwnd, 0, uintptr(offScreen), uintptr(offScreen), 0, 0, 0x0001)
	}

	// Open CONOUT$ and CONIN$ for the attached console
	conoutPtr, _ := syscall.UTF16PtrFromString("CONOUT$")
	hOut, err := syscall.CreateFile(conoutPtr,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
		nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		syscall.CloseHandle(syscall.Handle(pi.Thread))
		syscall.CloseHandle(syscall.Handle(pi.Process))
		procFreeConsoleConsole.Call()
		return nil, nil, fmt.Errorf("open CONOUT$: %w", err)
	}

	coninPtr, _ := syscall.UTF16PtrFromString("CONIN$")
	hIn, err := syscall.CreateFile(coninPtr,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
		nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		syscall.CloseHandle(hOut)
		syscall.CloseHandle(syscall.Handle(pi.Thread))
		syscall.CloseHandle(syscall.Handle(pi.Process))
		procFreeConsoleConsole.Call()
		return nil, nil, fmt.Errorf("open CONIN$: %w", err)
	}

	// Set buffer size
	bufSize := _COORD{X: int16(cols), Y: int16(rows * 10)}
	procSetConsoleScreenBufferSizeConsole.Call(
		uintptr(hOut), uintptr(*(*uint32)(unsafe.Pointer(&bufSize))),
	)

	winRect := _SMALL_RECT{Left: 0, Top: 0, Right: int16(cols) - 1, Bottom: int16(rows) - 1}
	procSetConsoleWindowInfoConsole.Call(
		uintptr(hOut), 1, uintptr(unsafe.Pointer(&winRect)),
	)

	d := &consoleDriver{
		processHandle: syscall.Handle(pi.Process),
		processID:     pi.ProcessID,
		conOut:        hOut,
		conIn:         hIn,
		cols:          cols,
		rows:          rows,
		stopCh:        make(chan struct{}),
	}
	return d, pi, nil
}

// charToVK maps common ASCII characters to Windows virtual key codes.
func charToVK(ch rune) uint16 {
	switch {
	case ch >= 'a' && ch <= 'z':
		return uint16(ch - 32)
	case ch >= 'A' && ch <= 'Z':
		return uint16(ch)
	case ch >= '0' && ch <= '9':
		return uint16(ch)
	case ch == ' ':
		return 0x20 // VK_SPACE
	case ch == '\t':
		return 0x09 // VK_TAB
	case ch == '\r':
		return 0x0D // VK_RETURN
	case ch == '\b':
		return 0x08 // VK_BACK
	case ch == 27:
		return 0x1B // VK_ESCAPE
	case ch == '\\':
		return 0xDC // VK_OEM_5
	case ch == '/':
		return 0xBF // VK_OEM_2
	case ch == ':':
		return 0xBA // VK_OEM_1
	case ch == ';':
		return 0xBA // VK_OEM_1
	case ch == '.':
		return 0xBE // VK_OEM_PERIOD
	case ch == ',':
		return 0xBC // VK_OEM_COMMA
	case ch == '-':
		return 0xBD // VK_OEM_MINUS
	case ch == '=':
		return 0xBB // VK_OEM_PLUS
	case ch == '[':
		return 0xDB // VK_OEM_4
	case ch == ']':
		return 0xDD // VK_OEM_6
	case ch == '\'':
		return 0xDE // VK_OEM_7
	case ch == '`':
		return 0xC0 // VK_OEM_3
	default:
		return uint16(ch)
	}
}
