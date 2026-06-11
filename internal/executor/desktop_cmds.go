package executor

import (
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

// ────────────────────────────────────────────────────────────────────────────
// desktop.clipboard.get / desktop.clipboard.set
// ────────────────────────────────────────────────────────────────────────────

type clipboardGetPayload struct{}
type clipboardSetPayload struct {
	Text string `json:"text"`
}

func clipboardGet(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	text, err := readClipboard()
	if err != nil {
		return nil, fmt.Errorf("clipboard read: %w", err)
	}
	return map[string]interface{}{
		"text":  text,
		"found": text != "",
	}, nil
}

func clipboardSet(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p clipboardSetPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if err := writeClipboard(p.Text); err != nil {
		return nil, fmt.Errorf("clipboard write: %w", err)
	}
	return map[string]interface{}{"status": "ok"}, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Platform-specific clipboard implementations
// ────────────────────────────────────────────────────────────────────────────

func readClipboard() (string, error) {
	switch runtime.GOOS {
	case "windows":
		return readClipboardWindows()
	case "darwin":
		return readClipboardMacOS()
	case "linux":
		return readClipboardLinux()
	default:
		return "", fmt.Errorf("clipboard not supported on %s", runtime.GOOS)
	}
}

func writeClipboard(text string) error {
	switch runtime.GOOS {
	case "windows":
		return writeClipboardWindows(text)
	case "darwin":
		return writeClipboardMacOS(text)
	case "linux":
		return writeClipboardLinux(text)
	default:
		return fmt.Errorf("clipboard not supported on %s", runtime.GOOS)
	}
}

// ── Windows ─────────────────────────────────────────────────────────────────

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

// ── macOS ───────────────────────────────────────────────────────────────────

func readClipboardMacOS() (string, error) {
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		return "", fmt.Errorf("pbpaste: %w", err)
	}
	return string(out), nil
}

func writeClipboardMacOS(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// ── Linux ───────────────────────────────────────────────────────────────────

func readClipboardLinux() (string, error) {
	if _, err := exec.LookPath("xclip"); err == nil {
		out, err := exec.Command("xclip", "-o", "-selection", "clipboard").Output()
		if err == nil {
			return string(out), nil
		}
	}
	if _, err := exec.LookPath("wl-paste"); err == nil {
		out, err := exec.Command("wl-paste").Output()
		if err == nil {
			return string(out), nil
		}
	}
	return "", fmt.Errorf("neither xclip nor wl-paste found")
}

func writeClipboardLinux(text string) error {
	if _, err := exec.LookPath("xclip"); err == nil {
		cmd := exec.Command("xclip", "-selection", "clipboard")
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	if _, err := exec.LookPath("wl-copy"); err == nil {
		cmd := exec.Command("wl-copy")
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	return fmt.Errorf("neither xclip nor wl-copy found")
}

// ────────────────────────────────────────────────────────────────────────────
// desktop.screenshot
// ────────────────────────────────────────────────────────────────────────────

type screenshotPayload struct {
	MonitorIndex int `json:"monitorIndex,omitempty"`
}

func screenshotCapture(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p screenshotPayload
	if req.Payload != nil {
		_ = decodePayload(req.Payload, &p)
	}

	img, err := captureScreen(p.MonitorIndex)
	if err != nil {
		return nil, fmt.Errorf("screenshot: %w", err)
	}

	var buf strings.Builder
	b64 := base64.NewEncoder(base64.StdEncoding, &buf)
	if err := png.Encode(b64, img); err != nil {
		return nil, fmt.Errorf("screenshot encode: %w", err)
	}
	b64.Close()

	return map[string]interface{}{
		"data":   "data:image/png;base64," + buf.String(),
		"width":  img.Bounds().Dx(),
		"height": img.Bounds().Dy(),
		"format": "png",
	}, nil
}

func captureScreen(monitorIndex int) (*image.RGBA, error) {
	switch runtime.GOOS {
	case "windows":
		return captureScreenWindows(monitorIndex)
	case "darwin":
		return captureScreenMacOS(monitorIndex)
	case "linux":
		return captureScreenLinux(monitorIndex)
	default:
		return nil, fmt.Errorf("screenshot not supported on %s", runtime.GOOS)
	}
}

// ── Windows screenshot (GDI) ────────────────────────────────────────────────

var (
	gdi32 = syscall.NewLazyDLL("gdi32.dll")

	procCreateDC              = gdi32.NewProc("CreateDCW")
	procCreateCompatibleDC    = gdi32.NewProc("CreateCompatibleDC")
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

// ── macOS screenshot ────────────────────────────────────────────────────────

func captureScreenMacOS(_ int) (*image.RGBA, error) {
	tmpFile, err := os.CreateTemp("", "sessionbridge-screenshot-*.png")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	if err := exec.Command("screencapture", "-x", tmpPath).Run(); err != nil {
		return nil, fmt.Errorf("screencapture: %w", err)
	}

	f, err := os.Open(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("open temp: %w", err)
	}
	defer f.Close()

	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode png: %w", err)
	}

	rgba := image.NewRGBA(img.Bounds())
	for y := 0; y < rgba.Bounds().Dy(); y++ {
		for x := 0; x < rgba.Bounds().Dx(); x++ {
			rgba.Set(x, y, img.At(x, y))
		}
	}
	return rgba, nil
}

// ── Linux screenshot ────────────────────────────────────────────────────────

func captureScreenLinux(_ int) (*image.RGBA, error) {
	if _, err := exec.LookPath("import"); err == nil {
		tmpFile, err := os.CreateTemp("", "sessionbridge-screenshot-*.png")
		if err != nil {
			return nil, fmt.Errorf("create temp file: %w", err)
		}
		tmpPath := tmpFile.Name()
		tmpFile.Close()
		defer os.Remove(tmpPath)

		if err := exec.Command("import", "-window", "root", tmpPath).Run(); err == nil {
			f, _ := os.Open(tmpPath)
			if f != nil {
				defer f.Close()
				img, err := png.Decode(f)
				if err == nil {
					rgba := image.NewRGBA(img.Bounds())
					for y := 0; y < rgba.Bounds().Dy(); y++ {
						for x := 0; x < rgba.Bounds().Dx(); x++ {
							rgba.Set(x, y, img.At(x, y))
						}
					}
					return rgba, nil
				}
			}
		}
	}

	if _, err := exec.LookPath("gnome-screenshot"); err == nil {
		tmpPath := filepath.Join(os.TempDir(), "sessionbridge-screenshot.png")
		if err := exec.Command("gnome-screenshot", "-f", tmpPath).Run(); err == nil {
			defer os.Remove(tmpPath)
			f, _ := os.Open(tmpPath)
			if f != nil {
				defer f.Close()
				img, err := png.Decode(f)
				if err == nil {
					rgba := image.NewRGBA(img.Bounds())
					for y := 0; y < rgba.Bounds().Dy(); y++ {
						for x := 0; x < rgba.Bounds().Dx(); x++ {
							rgba.Set(x, y, img.At(x, y))
						}
					}
					return rgba, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("no screenshot tool found (install 'import' from ImageMagick, 'gnome-screenshot', or 'screencapture')")
}
