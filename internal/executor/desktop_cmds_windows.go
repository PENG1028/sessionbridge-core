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
