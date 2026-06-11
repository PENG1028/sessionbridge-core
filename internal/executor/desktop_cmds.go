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
// Platform dispatch — each platform has its own implementation file.
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
