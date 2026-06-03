package main

import (
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

type _COORD struct{ X, Y int16 }
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
	_CREATE_NEW_CONSOLE = 0x00000010
	_SW_HIDE            = 0
)

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	user32                         = syscall.NewLazyDLL("user32.dll")
	procCreateProcessW             = kernel32.NewProc("CreateProcessW")
	procFreeConsole                = kernel32.NewProc("FreeConsole")
	procAttachConsole              = kernel32.NewProc("AttachConsole")
	procGetConsoleWindow           = kernel32.NewProc("GetConsoleWindow")
	procShowWindow                 = user32.NewProc("ShowWindow")
	procSetWindowPos               = user32.NewProc("SetWindowPos")
	procGetStdHandle               = kernel32.NewProc("GetStdHandle")
	procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
	procReadConsoleOutput          = kernel32.NewProc("ReadConsoleOutputW")
	procWriteConsoleInput          = kernel32.NewProc("WriteConsoleInputW")
	procSetConsoleScreenBufferSize = kernel32.NewProc("SetConsoleScreenBufferSize")
	procWaitForSingleObject        = kernel32.NewProc("WaitForSingleObject")
	procCloseHandle                = kernel32.NewProc("CloseHandle")
	procTerminateProcess           = kernel32.NewProc("TerminateProcess")
	procGetExitCodeProcess         = kernel32.NewProc("GetExitCodeProcess")
)

type _PROCESS_INFORMATION struct {
	Process   syscall.Handle
	Thread    syscall.Handle
	ProcessID uint32
	ThreadID  uint32
}

func main() {
	out, _ := os.Create("console_test_result.txt")
	defer out.Close()
	logf := func(format string, args ...interface{}) {
		s := fmt.Sprintf(format+"\n", args...)
		out.WriteString(s)
		os.Stdout.WriteString(s)
	}

	logf("=== Console Fallback Test (no suspend) ===\n")

	// First, detach from our own console
	procFreeConsole.Call()

	// Create cmd.exe with CREATE_NEW_CONSOLE (not suspended)
	cmdLine, _ := syscall.UTF16PtrFromString("cmd.exe /K echo HELLO_CONSOLE_TEST")
	si := &syscall.StartupInfo{}
	si.Cb = uint32(unsafe.Sizeof(*si))
	si.ShowWindow = _SW_HIDE
	si.Flags = 0x00000001 // STARTF_USESHOWWINDOW

	pi := &_PROCESS_INFORMATION{}

	flags := uint32(_CREATE_NEW_CONSOLE)
	r, _, e := procCreateProcessW.Call(
		0, uintptr(unsafe.Pointer(cmdLine)), 0, 0, 0, uintptr(flags),
		0, 0, uintptr(unsafe.Pointer(si)), uintptr(unsafe.Pointer(pi)),
	)
	if r == 0 {
		logf("FAIL CreateProcess: %v", e)
		return
	}
	logf("Created: PID=%d", pi.ProcessID)
	procCloseHandle.Call(uintptr(pi.Thread))

	// Wait briefly for console initialization
	time.Sleep(500 * time.Millisecond)

	// Try attaching to child console
	r, _, e = procAttachConsole.Call(uintptr(pi.ProcessID))
	if r == 0 {
		logf("FAIL AttachConsole: %v, lastErr=%d", e, syscall.GetLastError())

		// Alternative: use FreeConsole and see if we're already attached to something
		logf("Trying alternative approach...")

		// Check if child is still running
		var exitCode uint32
		procGetExitCodeProcess.Call(uintptr(pi.Process), uintptr(unsafe.Pointer(&exitCode)))
		logf("Child exit code: %d (259=STILL_ACTIVE)", exitCode)
		if exitCode != 259 {
			logf("Process already exited! Need /K flag for cmd.exe")
		}

		procTerminateProcess.Call(uintptr(pi.Process), 1)
		procCloseHandle.Call(uintptr(pi.Process))
		return
	}

	logf("Attached to child console (PID=%d)", pi.ProcessID)

	// Hide the console window
	childHwnd, _, _ := procGetConsoleWindow.Call()
	logf("Child console HWND: %v", childHwnd)
	if childHwnd != 0 {
		procShowWindow.Call(childHwnd, _SW_HIDE)
		logf("Hid console window")
	}

	// After AttachConsole, GetStdHandle returns handles from BEFORE the attach.
	// Must open CONOUT$ and CONIN$ explicitly to get handles for the attached console.
	conoutPtr, _ := syscall.UTF16PtrFromString("CONOUT$")
	hOut, err := syscall.CreateFile(conoutPtr, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		logf("FAIL CreateFile CONOUT$: %v", err)
		procFreeConsole.Call()
		return
	}
	coninPtr, _ := syscall.UTF16PtrFromString("CONIN$")
	hIn, err := syscall.CreateFile(coninPtr, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		logf("FAIL CreateFile CONIN$: %v", err)
		syscall.CloseHandle(hOut)
		procFreeConsole.Call()
		return
	}
	defer syscall.CloseHandle(hOut)
	defer syscall.CloseHandle(hIn)
	logf("Console handles: hOut=%v hIn=%v", hOut, hIn)

	// Read buffer info
	var csbi _CONSOLE_SCREEN_BUFFER_INFO
	r, _, _ = procGetConsoleScreenBufferInfo.Call(uintptr(hOut), uintptr(unsafe.Pointer(&csbi)))
	if r != 0 {
		logf("Buffer: size=%dx%d cursor=%d,%d",
			csbi.Size.X, csbi.Size.Y, csbi.CursorPosition.X, csbi.CursorPosition.Y)

		// Read the screen buffer
		readRegion := _SMALL_RECT{Left: 0, Top: 0, Right: csbi.Size.X - 1, Bottom: csbi.CursorPosition.Y}
		if readRegion.Bottom < 5 {
			readRegion.Bottom = 5
		}
		bufRows := readRegion.Bottom + 1
		buf := make([]_CHAR_INFO, int(csbi.Size.X)*int(bufRows))
		bufCoord := _COORD{X: csbi.Size.X, Y: int16(bufRows)}
		r, _, e = procReadConsoleOutput.Call(
			uintptr(hOut),
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(*(*uint32)(unsafe.Pointer(&bufCoord))),
			uintptr(*(*uint32)(unsafe.Pointer(&_COORD{X: 0, Y: 0}))),
			uintptr(unsafe.Pointer(&readRegion)),
		)
		if r != 0 {
			logf("Console output:")
			for row := int16(0); row < int16(bufRows); row++ {
				line := ""
				for col := int16(0); col < csbi.Size.X; col++ {
					ch := buf[int(row)*int(csbi.Size.X)+int(col)].Char
					if ch == 0 {
						if line == "" {
							continue
						}
						break
					}
					line += string(rune(ch))
				}
				if line != "" {
					logf("  [%02d] %s", row, line)
				}
			}
		} else {
			logf("ReadConsoleOutput failed: %v", e)
		}
	} else {
		logf("GetConsoleScreenBufferInfo failed")
	}

	// Detach
	procFreeConsole.Call()
	logf("Detached from console")

	// Cleanup
	procTerminateProcess.Call(uintptr(pi.Process), 1)
	procWaitForSingleObject.Call(uintptr(pi.Process), uintptr(2000))
	procCloseHandle.Call(uintptr(pi.Process))
	logf("Done.")
}
