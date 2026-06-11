//go:build windows

package executor

import (
	"fmt"
	"image"
	"syscall"
	"unsafe"
)

// ── Windows clipboard (Win32 API) ─────────────────────────────────────────

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procOpenClipboard     = user32.NewProc("OpenClipboard")
	procCloseClipboard    = user32.NewProc("CloseClipboard")
	procEmptyClipboard    = user32.NewProc("EmptyClipboard")
	procGetClipboardData  = user32.NewProc("GetClipboardData")
	procSetClipboardData  = user32.NewProc("SetClipboardData")
	procGlobalAlloc       = kernel32.NewProc("GlobalAlloc")
	procGlobalLock        = kernel32.NewProc("GlobalLock")
	procGlobalUnlock      = kernel32.NewProc("GlobalUnlock")
	procGlobalSize        = kernel32.NewProc("GlobalSize")

	// Input
	procSendInput   = user32.NewProc("SendInput")
	procGetAsyncKey = user32.NewProc("GetAsyncKeyState")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
	gmemZeroInit  = 0x0040
)

func readClipboardWindows() (string, error) {
	r, _, _ := procOpenClipboard.Call(0)
	if r == 0 {
		return "", fmt.Errorf("OpenClipboard failed")
	}
	defer procCloseClipboard.Call()

	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", nil
	}

	size, _, _ := procGlobalSize.Call(h)
	if size == 0 {
		return "", nil
	}

	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return "", fmt.Errorf("GlobalLock failed")
	}
	defer procGlobalUnlock.Call(h)

	buf := make([]uint16, size/2)
	copy(buf, unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), len(buf)))

	nullIdx := len(buf)
	for i, v := range buf {
		if v == 0 {
			nullIdx = i
			break
		}
	}

	return syscall.UTF16ToString(buf[:nullIdx]), nil
}

func writeClipboardWindows(text string) error {
	r, _, _ := procOpenClipboard.Call(0)
	if r == 0 {
		return fmt.Errorf("OpenClipboard failed")
	}
	defer procCloseClipboard.Call()

	procEmptyClipboard.Call()

	utf16Str := utf16Encode(text + "\x00")
	size := len(utf16Str) * 2

	h, _, _ := procGlobalAlloc.Call(gmemMoveable|gmemZeroInit, uintptr(size))
	if h == 0 {
		return fmt.Errorf("GlobalAlloc failed")
	}

	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return fmt.Errorf("GlobalLock failed")
	}

	copy(unsafe.Slice((*byte)(unsafe.Pointer(ptr)), size), utf16Bytes(utf16Str))
	procGlobalUnlock.Call(h)

	r, _, _ = procSetClipboardData.Call(cfUnicodeText, h)
	if r == 0 {
		return fmt.Errorf("SetClipboardData failed")
	}
	return nil
}

func utf16Encode(s string) []uint16 {
	u, _ := syscall.UTF16FromString(s)
	return u
}

func utf16Bytes(u []uint16) []byte {
	b := make([]byte, len(u)*2)
	for i, v := range u {
		b[i*2] = byte(v)
		b[i*2+1] = byte(v >> 8)
	}
	return b
}

// ── Windows screenshot (GDI) ────────────────────────────────────────────────

var (
	gdi32 = syscall.NewLazyDLL("gdi32.dll")

	procCreateDC              = gdi32.NewProc("CreateDCW")
	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procBitBlt                = gdi32.NewProc("BitBlt")
	procGetDIBits             = gdi32.NewProc("GetDIBits")
	procDeleteDC              = gdi32.NewProc("DeleteDC")
	procDeleteObject          = gdi32.NewProc("DeleteObject")
	procSelectObject          = gdi32.NewProc("SelectObject")
	procReleaseDC             = user32.NewProc("ReleaseDC")
	procGetDC                 = user32.NewProc("GetDC")
	procGetSystemMetrics      = user32.NewProc("GetSystemMetrics")
)

const (
	SRCCOPY     = 0x00CC0020
	SM_CXSCREEN = 0
	SM_CYSCREEN = 1
)

type _BITMAPINFOHEADER struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type _BITMAPINFO struct {
	Header _BITMAPINFOHEADER
	Colors [1]uint32
}

func captureScreenWindows(_ int) (*image.RGBA, error) {
	desk, _, _ := procGetDC.Call(0)
	if desk == 0 {
		return nil, fmt.Errorf("GetDC failed")
	}
	defer procReleaseDC.Call(0, desk)

	r1, _, _ := procGetSystemMetrics.Call(SM_CXSCREEN)
	width := int(r1)
	r1, _, _ = procGetSystemMetrics.Call(SM_CYSCREEN)
	height := int(r1)

	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid screen dimensions: %dx%d", width, height)
	}

	hdc, _, _ := procCreateCompatibleDC.Call(desk)
	if hdc == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed")
	}
	defer procDeleteDC.Call(hdc)

	hbmp, _, _ := procCreateCompatibleBitmap.Call(desk, uintptr(width), uintptr(height))
	if hbmp == 0 {
		return nil, fmt.Errorf("CreateCompatibleBitmap failed")
	}
	defer procDeleteObject.Call(hbmp)

	procSelectObject.Call(hdc, hbmp)

	r1, _, _ = procBitBlt.Call(hdc, 0, 0, uintptr(width), uintptr(height),
		desk, 0, 0, SRCCOPY)
	if r1 == 0 {
		return nil, fmt.Errorf("BitBlt failed")
	}

	var bmi _BITMAPINFO
	bmi.Header.Size = uint32(unsafe.Sizeof(bmi.Header))
	bmi.Header.Width = int32(width)
	bmi.Header.Height = -int32(height)
	bmi.Header.Planes = 1
	bmi.Header.BitCount = 32
	bmi.Header.Compression = 0

	pixels := make([]byte, width*height*4)

	r1, _, _ = procGetDIBits.Call(hdc, hbmp, 0, uintptr(height),
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&bmi)), 0)
	if r1 == 0 {
		return nil, fmt.Errorf("GetDIBits failed")
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			idx := (y*width + x) * 4
			img.Pix[img.PixOffset(x, y)] = pixels[idx+2]
			img.Pix[img.PixOffset(x, y)+1] = pixels[idx+1]
			img.Pix[img.PixOffset(x, y)+2] = pixels[idx]
			img.Pix[img.PixOffset(x, y)+3] = 255
		}
	}

	return img, nil
}

// ── Windows keyboard (SendInput) ─────────────────────────────────────────

// Win32 virtual key codes
const (
	vkBack    = 0x08
	vkTab     = 0x09
	vkReturn  = 0x0D
	vkShift   = 0x10
	vkControl = 0x11
	vkMenu    = 0x12
	vkPause   = 0x13
	vkEscape  = 0x1B
	vkSpace   = 0x20
	vkLeft    = 0x25
	vkUp      = 0x26
	vkRight   = 0x27
	vkDown    = 0x28
	vkDelete  = 0x2E
	vkLWin    = 0x5B
	vkCapital = 0x14
	vkHome    = 0x24
	vkEnd     = 0x23
	vkPrior   = 0x21
	vkNext    = 0x22
	vkF1      = 0x70
)

const (
	keyeventfKeyDown = 0x0000
	keyeventfKeyUp   = 0x0002
	keyeventfUnicode = 0x0004
)

const inputKeyboard = 1

// windowVK maps key names to Windows virtual key codes.
var windowsVK = map[string]uint16{
	"enter": vkReturn, "return": vkReturn, "tab": vkTab,
	"space": vkSpace, "escape": vkEscape, "esc": vkEscape,
	"backspace": vkBack, "delete": vkDelete,
	"home": vkHome, "end": vkEnd, "pageup": vkPrior, "pagedown": vkNext,
	"up": vkUp, "down": vkDown, "left": vkLeft, "right": vkRight,
	"ctrl": vkControl, "alt": vkMenu, "shift": vkShift,
	"capslock": vkCapital, "pause": vkPause,
	"f1": vkF1, "f2": vkF1 + 1, "f3": vkF1 + 2, "f4": vkF1 + 3,
	"f5": vkF1 + 4, "f6": vkF1 + 5, "f7": vkF1 + 6, "f8": vkF1 + 7,
	"f9": vkF1 + 8, "f10": vkF1 + 9, "f11": vkF1 + 10, "f12": vkF1 + 11,
	"win": vkLWin, "meta": vkLWin,
}

// keyboardWindows sends keystrokes on Windows using SendInput.
func keyboardWindows(action string, keys []string) error {
	for _, key := range keys {
		var vk uint16
		var isUnicode bool

		if len(key) == 1 && (key[0] >= 'a' && key[0] <= 'z') {
			vk = uint16(key[0] - 0x20) // 'a' → VK_A (0x41)
		} else if len(key) == 1 && key[0] >= '0' && key[0] <= '9' {
			vk = uint16(key[0]) // digit VK codes match ASCII
		} else if code, ok := windowsVK[key]; ok {
			vk = code
		} else if len(key) == 1 {
			// Unicode character — use KEYEVENTF_UNICODE
			vk = 0
			isUnicode = true
		} else {
			return fmt.Errorf("unknown key: %s", key)
		}

		switch action {
		case "tap", "press":
			if isUnicode {
				sendUnicodeTap(uint16(key[0]))
			} else {
				sendVKTap(vk)
			}
		default:
			return fmt.Errorf("keyboard action %q not supported", action)
		}
	}
	return nil
}

// sendVKTap sends a key-down + key-up for a virtual key code.
func sendVKTap(vk uint16) {
	if vk == 0 {
		return
	}
	// Down
	sendInput(inputKeyboard, &keybd{vk: vk})
	// Up
	sendInput(inputKeyboard, &keybd{vk: vk, flags: keyeventfKeyUp})
}

// sendUnicodeTap sends a Unicode character via KEYEVENTF_UNICODE.
func sendUnicodeTap(char uint16) {
	// Down with Unicode scan code
	sendInput(inputKeyboard, &keybd{scan: char, flags: keyeventfUnicode})
	// Up with Unicode scan code
	sendInput(inputKeyboard, &keybd{scan: char, flags: keyeventfUnicode | keyeventfKeyUp})
}

// keybd mirrors Win32 KEYBDINPUT layout on x64:
//
//	Off  Size  Field
//	0    2     wVk
//	2    2     wScan
//	4    4     dwFlags
//	8    4     time
//	12   4     padding (ULONG_PTR alignment)
//	16   8     dwExtraInfo
//	Total: 24 bytes
type keybd struct {
	vk         uint16
	scan       uint16
	flags      uint32
	time       uint32
	_          uint32 // padding
	dwExtraInfo uint64
}

// sendInput wraps the Win32 SendInput API for a single input event.
func sendInput(typ int, k *keybd) {
	// INPUT layout on x64:
	// Off  Size  Field
	// 0    4     DWORD type
	// 4    4     padding
	// 8    24    KEYBDINPUT (or MOUSEINPUT)
	// Total: 32 bytes for keyboard, 40 for mouse

	var buf [40]byte
	buf[0] = byte(typ) // type = INPUT_KEYBOARD (1)

	// Copy keybd struct starting at offset 8
	kBytes := (*[24]byte)(unsafe.Pointer(k))
	copy(buf[8:32], kBytes[:])

	procSendInput.Call(1, uintptr(unsafe.Pointer(&buf[0])), uintptr(32))
}

// ── Windows mouse (SendInput) ─────────────────────────────────────────────

type mouseSt struct {
	x          int32
	y          int32
	mouseData  uint32
	flags      uint32
	time       uint32
	_          uint32
	dwExtraInfo uint64
}

const inputMouse = 0

const (
	mouseeventfMove      = 0x0001
	mouseeventfLeftDown  = 0x0002
	mouseeventfLeftUp    = 0x0004
	mouseeventfRightDown = 0x0008
	mouseeventfRightUp   = 0x0010
	mouseeventfMiddleDown = 0x0020
	mouseeventfMiddleUp  = 0x0040
	mouseeventfAbsolute  = 0x8000
	mouseeventfWheel     = 0x0800
)

func mouseWindows(action string, x, y int, button string, amount int) error {
	switch action {
	case "move":
		return mouseMove(x, y)
	case "click":
		return mouseClick(x, y, button)
	case "scroll":
		return mouseScroll(amount)
	default:
		return fmt.Errorf("mouse action %q not supported on Windows", action)
	}
}

func mouseMove(x, y int) error {
	// Normalize to absolute coordinates (0-65535)
	absX := uint32(x * 65535 / 1920)  // FIXME: get actual screen width
	absY := uint32(y * 65535 / 1080)  // FIXME: get actual screen height
	sendMouse(mouseeventfMove|mouseeventfAbsolute, int32(absX), int32(absY), 0, 0)
	return nil
}

func mouseClick(x, y int, button string) error {
	if x >= 0 && y >= 0 {
		mouseMove(x, y)
	}
	var down, up uint32
	switch button {
	case "right":
		down = mouseeventfRightDown
		up = mouseeventfRightUp
	case "middle":
		down = mouseeventfMiddleDown
		up = mouseeventfMiddleUp
	default:
		down = mouseeventfLeftDown
		up = mouseeventfLeftUp
	}
	sendMouse(down, 0, 0, 0, 0)
	sendMouse(up, 0, 0, 0, 0)
	return nil
}

func mouseScroll(amount int) error {
	// WHEEL_DELTA = 120, positive = scroll up, negative = scroll down
	data := uint32(amount * 120)
	sendMouse(mouseeventfWheel, 0, 0, data, 0)
	return nil
}

func sendMouse(flags uint32, x, y int32, mouseData uint32, time uint32) {
	// MOUSEINPUT layout on x64:
	// Off  Size  Field
	// 0    4     dx
	// 4    4     dy
	// 8    4     mouseData
	// 12   4     dwFlags
	// 16   4     time
	// 20   4     padding
	// 24   8     dwExtraInfo
	// Total: 32 bytes

	var buf [40]byte
	buf[0] = inputMouse // type = INPUT_MOUSE

	m := mouseSt{
		x:          x,
		y:          y,
		mouseData:  mouseData,
		flags:      flags,
		time:       time,
	}
	mBytes := (*[32]byte)(unsafe.Pointer(&m))
	copy(buf[8:40], mBytes[:])

	procSendInput.Call(1, uintptr(unsafe.Pointer(&buf[0])), uintptr(40))
}

// ── Windows type text ─────────────────────────────────────────────────────

func typeTextWindows(text string) error {
	for _, r := range text {
		sendUnicodeTap(uint16(r))
	}
	return nil
}
