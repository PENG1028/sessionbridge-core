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

// ────────────────────────────────────────────────────────────────────────────
// input.keyboard — send keystrokes
// ────────────────────────────────────────────────────────────────────────────

type keyboardInputPayload struct {
	Action string   `json:"action"` // "tap" | "press" | "release"
	Keys   []string `json:"keys"`   // key names: "a", "ctrl", "enter", "f1", etc.
}

func keyboardInput(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p keyboardInputPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if len(p.Keys) == 0 {
		return nil, fmt.Errorf("keys required")
	}

	return map[string]interface{}{"status": "ok"}, keyboardInputImpl(p.Action, p.Keys)
}

func keyboardInputImpl(action string, keys []string) error {
	switch runtime.GOOS {
	case "windows":
		return keyboardWindows(action, keys)
	case "darwin":
		return keyboardMacOS(action, keys)
	case "linux":
		return keyboardLinux(action, keys)
	default:
		return fmt.Errorf("keyboard input not supported on %s", runtime.GOOS)
	}
}

// ── macOS keyboard ─────────────────────────────────────────────────────────

func keyboardMacOS(action string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	switch action {
	case "tap":
		if len(keys) == 1 && isSimpleChar(keys[0]) {
			return exec.Command("osascript", "-e",
				`tell app "System Events" to keystroke "`+escapeAppleScript(keys[0])+`"`).Run()
		}
		// Key code or combination
		return exec.Command("osascript", "-e",
			`tell app "System Events" to key code `+macOSKeyCode(keys[0])).Run()
	default:
		return fmt.Errorf("keyboard action %q not supported on macOS (use 'tap')", action)
	}
}

func isSimpleChar(s string) bool {
	if len(s) != 1 {
		return false
	}
	b := s[0]
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == ' ' || b == '-' || b == '_'
}

func escapeAppleScript(s string) string {
	r := strings.NewReplacer("\\", "\\\\", `"`, `\"`, "\n", "\\n")
	return r.Replace(s)
}

func macOSKeyCode(key string) string {
	// Common key codes for macOS
	codes := map[string]string{
		"enter": "36", "return": "36", "tab": "48", "space": "49",
		"delete": "51", "backspace": "51", "escape": "53", "esc": "53",
		"home": "115", "end": "119", "pageup": "116", "pagedown": "121",
		"up": "126", "down": "125", "left": "123", "right": "124",
		"f1": "122", "f2": "120", "f3": "99", "f4": "118",
		"f5": "96", "f6": "97", "f7": "98", "f8": "100",
		"f9": "101", "f10": "109", "f11": "103", "f12": "111",
	}
	if code, ok := codes[key]; ok {
		return code
	}
	return "0" // fallback
}

// ── Linux keyboard ─────────────────────────────────────────────────────────

func keyboardLinux(action string, keys []string) error {
	tool := pickTool("xdotool")
	if tool == "" {
		return fmt.Errorf("neither xdotool found for keyboard input")
	}
	switch action {
	case "tap":
		args := []string{"key"}
		args = append(args, keys...)
		return exec.Command(tool, args...).Run()
	default:
		return fmt.Errorf("keyboard action %q not supported on Linux (use 'tap')", action)
	}
}

func pickTool(names ...string) string {
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// ────────────────────────────────────────────────────────────────────────────
// input.mouse — mouse operations
// ────────────────────────────────────────────────────────────────────────────

type mouseInputPayload struct {
	Action string `json:"action"` // "move" | "click" | "scroll" | "press" | "release"
	X      int    `json:"x,omitempty"`
	Y      int    `json:"y,omitempty"`
	Button string `json:"button,omitempty"` // "left" | "right" | "middle"
	Amount int    `json:"amount,omitempty"` // scroll lines
}

func mouseInput(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p mouseInputPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	return map[string]interface{}{"status": "ok"}, mouseInputImpl(p.Action, p.X, p.Y, p.Button, p.Amount)
}

func mouseInputImpl(action string, x, y int, button string, amount int) error {
	switch runtime.GOOS {
	case "windows":
		return mouseWindows(action, x, y, button, amount)
	case "darwin":
		return mouseMacOS(action, x, y, button, amount)
	case "linux":
		return mouseLinux(action, x, y, button, amount)
	default:
		return fmt.Errorf("mouse input not supported on %s", runtime.GOOS)
	}
}

// ── macOS mouse ────────────────────────────────────────────────────────────

func mouseMacOS(action string, x, y int, button string, amount int) error {
	switch action {
	case "click":
		return exec.Command("osascript", "-e",
			fmt.Sprintf(`tell app "System Events" to click at {%d,%d}`, x, y)).Run()
	default:
		return fmt.Errorf("mouse action %q not supported on macOS", action)
	}
}

// ── Linux mouse ────────────────────────────────────────────────────────────

func mouseLinux(action string, x, y int, button string, amount int) error {
	tool := pickTool("xdotool")
	if tool == "" {
		return fmt.Errorf("xdotool not found for mouse input")
	}
	switch action {
	case "move":
		return exec.Command(tool, "mousemove", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y)).Run()
	case "click":
		btn := "1" // left
		switch button {
		case "right":
			btn = "3"
		case "middle":
			btn = "2"
		}
		if x >= 0 && y >= 0 {
			exec.Command(tool, "mousemove", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y)).Run()
		}
		return exec.Command(tool, "click", btn).Run()
	case "scroll":
		dir := "--"
		if amount > 0 {
			dir = "--clearmodifiers"
		}
		return exec.Command(tool, "click", dir, fmt.Sprintf("%d", amount)).Run()
	default:
		return fmt.Errorf("mouse action %q not supported on Linux", action)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// desktop.click — convenience: move mouse then click
// ────────────────────────────────────────────────────────────────────────────

type desktopClickPayload struct {
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Button string `json:"button,omitempty"`
}

func desktopClick(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p desktopClickPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	return map[string]interface{}{"status": "ok"}, mouseInputImpl("click", p.X, p.Y, p.Button, 0)
}

// ────────────────────────────────────────────────────────────────────────────
// desktop.type — type text via keyboard
// ────────────────────────────────────────────────────────────────────────────

type desktopTypePayload struct {
	Text string `json:"text"`
}

func desktopType(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p desktopTypePayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.Text == "" {
		return nil, fmt.Errorf("text required")
	}
	return map[string]interface{}{"status": "ok"}, typeTextImpl(p.Text)
}

func typeTextImpl(text string) error {
	switch runtime.GOOS {
	case "windows":
		return typeTextWindows(text)
	case "darwin":
		return typeTextMacOS(text)
	case "linux":
		return typeTextLinux(text)
	default:
		return fmt.Errorf("text input not supported on %s", runtime.GOOS)
	}
}

// ── macOS type text ────────────────────────────────────────────────────────

func typeTextMacOS(text string) error {
	escaped := escapeAppleScript(text)
	return exec.Command("osascript", "-e",
		`tell app "System Events" to keystroke "`+escaped+`"`).Run()
}

// ── Linux type text ────────────────────────────────────────────────────────

func typeTextLinux(text string) error {
	tool := pickTool("xdotool")
	if tool == "" {
		return fmt.Errorf("xdotool not found for text input")
	}
	cmd := exec.Command(tool, "type", "--", text)
	return cmd.Run()
}
