//go:build windows

package process

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/user/sessionnode/go-core/pkg/types"
)

// ── Windows types for ConPTY ───────────────────────────────────

// _COORD is a Windows COORD structure (character coordinate).
type _COORD struct {
	X, Y int16
}

// _HPCON is a handle to a pseudo console (opaque).
type _HPCON uintptr

const (
	_PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE = 0x00020016 // ProcThreadAttributeValue(22, FALSE, TRUE, FALSE)
	_EXTENDED_STARTUPINFO_PRESENT        = 0x00080000
	_CREATE_UNICODE_ENVIRONMENT          = 0x00000400
	_CREATE_NO_WINDOW                    = 0x08000000
	_STARTF_USESTDHANDLES                = 0x00000100
	_PIPE_ACCESS_DUPLEX                  = 0x00000003
	_FILE_FLAG_FIRST_PIPE_INSTANCE       = 0x00080000
	_PIPE_TYPE_BYTE                      = 0x00000000
	_PIPE_READMODE_BYTE                  = 0x00000000
	_PIPE_WAIT                           = 0x00000000
	_ERROR_PIPE_CONNECTED                = 535
	_INVALID_HANDLE_VALUE                = ^uintptr(0)
)

// _STARTUPINFOEX wraps Windows STARTUPINFOEXW.
// The layout must match the OS definition byte-for-byte:
//
//	STARTUPINFOW  StartupInfo
//	LPPROC_THREAD_ATTRIBUTE_LIST lpAttributeList   (pointer, not inline)
type _STARTUPINFOEX struct {
	StartupInfo     syscall.StartupInfo
	lpAttributeList uintptr
}

// _PROCESS_INFORMATION wraps Windows PROCESS_INFORMATION.
type _PROCESS_INFORMATION struct {
	Process   syscall.Handle
	Thread    syscall.Handle
	ProcessID uint32
	ThreadID  uint32
}

// ── ConPTY proc resolution ─────────────────────────────────────
//
// On Windows systems where the in-box ConPTY is broken (e.g. Insider
// builds missing conpty.dll / condrv.sys), we load a bundled copy of
// conpty.dll + OpenConsole.exe (sourced from Windows Terminal / VS Code).
// The bundled conpty.dll exports the Conpty* API used by node-pty.

var (
	// ConPTY functions — may come from bundled conpty.dll or kernel32.dll
	procCreatePseudoConsole  *syscall.LazyProc
	procResizePseudoConsole  *syscall.LazyProc
	procClosePseudoConsole   *syscall.LazyProc
	procReleasePseudoConsole *syscall.LazyProc
	usingBundledConPTY       bool
	pipeCounter              atomic.Uint64

	// These always come from kernel32.dll
	modKernel32                           = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleCtrlHandler             = modKernel32.NewProc("SetConsoleCtrlHandler")
	procInitializeProcThreadAttributeList = modKernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttribute         = modKernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttributeList     = modKernel32.NewProc("DeleteProcThreadAttributeList")
	procCreateProcessW                    = modKernel32.NewProc("CreateProcessW")
	procCreateNamedPipeW                  = modKernel32.NewProc("CreateNamedPipeW")
	procConnectNamedPipe                  = modKernel32.NewProc("ConnectNamedPipe")
	procCancelIoEx                        = modKernel32.NewProc("CancelIoEx")
)

func init() {
	// Try bundled conpty.dll first (works on broken Insider builds).
	// conpty.dll and OpenConsole.exe must be siblings in the same directory.
	var searchPaths []string

	// 1. Relative to the executable (production deployment).
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		searchPaths = append(searchPaths,
			filepath.Join(exeDir, "bundled", "conpty.dll"),
			filepath.Join(exeDir, "conpty.dll"),
		)
	}

	// 2. Relative to working directory (development / test).
	if wd, err := os.Getwd(); err == nil {
		searchPaths = append(searchPaths,
			filepath.Join(wd, "bundled", "conpty.dll"),
			filepath.Join(wd, "..", "..", "bundled", "conpty.dll"), // from internal/process/ during test
			filepath.Join(wd, "conpty.dll"),
		)
	}

	// 3. Bare filename (PATH search).
	searchPaths = append(searchPaths, "conpty.dll")

	conptyDLL := modKernel32 // fallback: use kernel32
	usingBundled := false
	for _, p := range searchPaths {
		if _, err := os.Stat(p); err == nil {
			conptyDLL = syscall.NewLazyDLL(p)
			usingBundled = true
			log.Printf("[process] using bundled ConPTY: %s", p)
			break
		}
	}

	if usingBundled {
		usingBundledConPTY = true
		procCreatePseudoConsole = conptyDLL.NewProc("ConptyCreatePseudoConsole")
		procResizePseudoConsole = conptyDLL.NewProc("ConptyResizePseudoConsole")
		procClosePseudoConsole = conptyDLL.NewProc("ConptyClosePseudoConsole")
		procReleasePseudoConsole = conptyDLL.NewProc("ConptyReleasePseudoConsole")
	} else {
		procCreatePseudoConsole = conptyDLL.NewProc("CreatePseudoConsole")
		procResizePseudoConsole = conptyDLL.NewProc("ResizePseudoConsole")
		procClosePseudoConsole = conptyDLL.NewProc("ClosePseudoConsole")
	}
}

// ── conPTYDriver ───────────────────────────────────────────────

// conPTYDriver implements PTYDriver for Windows using the ConPTY API
// (available since Windows 10 1809 / build 17763).
type conPTYDriver struct {
	hpc          _HPCON
	inputPipe    syscall.Handle // write end — sends keyboard input to pseudo console
	outputPipe   syscall.Handle // read end  — receives screen output from pseudo console
	conptyInput  syscall.Handle
	conptyOutput syscall.Handle
}

type conPTYReadResult struct {
	data []byte
	err  error
}

func (d *conPTYDriver) Write(data string) error {
	if d.inputPipe == 0 {
		return fmt.Errorf("ConPTY input pipe not open")
	}
	var written uint32
	buf := []byte(data)
	return syscall.WriteFile(d.inputPipe, buf, &written, nil)
}

func (d *conPTYDriver) Resize(cols, rows int) error {
	if d.hpc == 0 {
		return fmt.Errorf("ConPTY handle is zero")
	}
	size := _COORD{X: int16(cols), Y: int16(rows)}
	// Pack COORD into a uint32 as Windows expects (two int16s).
	r, _, err := procResizePseudoConsole.Call(
		uintptr(d.hpc),
		uintptr(*(*uint32)(unsafe.Pointer(&size))),
	)
	if r == 0 {
		return nil // S_OK
	}
	return fmt.Errorf("ResizePseudoConsole: %w", err)
}

func (d *conPTYDriver) Close() error {
	if d.hpc != 0 {
		procClosePseudoConsole.Call(uintptr(d.hpc))
		d.hpc = 0
	}
	if d.inputPipe != 0 {
		syscall.CloseHandle(d.inputPipe)
		d.inputPipe = 0
	}
	if d.outputPipe != 0 {
		// Cancel any pending ReadFile before closing, otherwise
		// CloseHandle blocks indefinitely on this handle.
		procCancelIoEx.Call(uintptr(d.outputPipe), 0)
		syscall.CloseHandle(d.outputPipe)
		d.outputPipe = 0
	}
	if d.conptyInput != 0 {
		syscall.CloseHandle(d.conptyInput)
		d.conptyInput = 0
	}
	if d.conptyOutput != 0 {
		syscall.CloseHandle(d.conptyOutput)
		d.conptyOutput = 0
	}
	return nil
}

func (d *conPTYDriver) PtyMode() string { return "conpty" }

func (d *conPTYDriver) startOutputReader() <-chan conPTYReadResult {
	ch := make(chan conPTYReadResult, 1024)
	go func() {
		defer close(ch)
		defer syscall.CloseHandle(d.outputPipe)

		buf := make([]byte, 32*1024)
		for {
			var n uint32
			err := syscall.ReadFile(d.outputPipe, buf, &n, nil)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				ch <- conPTYReadResult{data: data}
			}
			if err != nil {
				ch <- conPTYReadResult{err: err}
				return
			}
		}
	}()
	return ch
}

// ── ConPTY creation ────────────────────────────────────────────

// createConPTY attempts to create a Windows Pseudo Console.
// Returns nil, error if ConPTY is not available on this system
// (pre-Win10-1809), allowing the caller to fall back to pipe mode.
func createConPTY(cols, rows int) (*conPTYDriver, error) {
	// Check that ConPTY procs are available (Win10 1809+).
	if err := procCreatePseudoConsole.Find(); err != nil {
		return nil, fmt.Errorf("CreatePseudoConsole not available: %w", err)
	}

	inputServer, inputName, err := createConPTYServerPipe("in")
	if err != nil {
		return nil, fmt.Errorf("create input named pipe: %w", err)
	}
	outputServer, outputName, err := createConPTYServerPipe("out")
	if err != nil {
		syscall.CloseHandle(inputServer)
		return nil, fmt.Errorf("create output named pipe: %w", err)
	}

	size := _COORD{X: int16(cols), Y: int16(rows)}
	var hpc _HPCON

	r, _, err := procCreatePseudoConsole.Call(
		uintptr(*(*uint32)(unsafe.Pointer(&size))),
		uintptr(inputServer),
		uintptr(outputServer),
		0,
		uintptr(unsafe.Pointer(&hpc)),
	)
	if r != 0 { // HRESULT failure
		syscall.CloseHandle(inputServer)
		syscall.CloseHandle(outputServer)
		return nil, fmt.Errorf("CreatePseudoConsole failed: %w", err)
	}

	inputClient, err := openConPTYPipeClient(inputName)
	if err != nil {
		procClosePseudoConsole.Call(uintptr(hpc))
		syscall.CloseHandle(inputServer)
		syscall.CloseHandle(outputServer)
		return nil, fmt.Errorf("open input pipe client: %w", err)
	}
	outputClient, err := openConPTYPipeClient(outputName)
	if err != nil {
		procClosePseudoConsole.Call(uintptr(hpc))
		syscall.CloseHandle(inputServer)
		syscall.CloseHandle(inputClient)
		syscall.CloseHandle(outputServer)
		return nil, fmt.Errorf("open output pipe client: %w", err)
	}

	if err := connectNamedPipe(inputServer); err != nil {
		procClosePseudoConsole.Call(uintptr(hpc))
		syscall.CloseHandle(inputServer)
		syscall.CloseHandle(inputClient)
		syscall.CloseHandle(outputServer)
		syscall.CloseHandle(outputClient)
		return nil, fmt.Errorf("connect input named pipe: %w", err)
	}
	if err := connectNamedPipe(outputServer); err != nil {
		procClosePseudoConsole.Call(uintptr(hpc))
		syscall.CloseHandle(inputServer)
		syscall.CloseHandle(inputClient)
		syscall.CloseHandle(outputServer)
		syscall.CloseHandle(outputClient)
		return nil, fmt.Errorf("connect output named pipe: %w", err)
	}

	return &conPTYDriver{
		hpc:          hpc,
		inputPipe:    inputClient,
		outputPipe:   outputClient,
		conptyInput:  inputServer,
		conptyOutput: outputServer,
	}, nil
}

func createConPTYServerPipe(kind string) (syscall.Handle, string, error) {
	name := fmt.Sprintf(`\\.\pipe\sessionbridge-conpty-%d-%d-%s`, os.Getpid(), pipeCounter.Add(1), kind)
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, "", err
	}

	r, _, callErr := procCreateNamedPipeW.Call(
		uintptr(unsafe.Pointer(namePtr)),
		_PIPE_ACCESS_DUPLEX|_FILE_FLAG_FIRST_PIPE_INSTANCE,
		_PIPE_TYPE_BYTE|_PIPE_READMODE_BYTE|_PIPE_WAIT,
		1,
		128*1024,
		128*1024,
		30000,
		0,
	)
	if r == _INVALID_HANDLE_VALUE {
		return 0, "", callErr
	}
	return syscall.Handle(r), name, nil
}

func openConPTYPipeClient(name string) (syscall.Handle, error) {
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	client, err := syscall.CreateFile(
		namePtr,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
		nil,
		syscall.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return 0, err
	}
	return client, nil
}

func connectNamedPipe(h syscall.Handle) error {
	r, _, err := procConnectNamedPipe.Call(uintptr(h), 0)
	if r != 0 || err == syscall.Errno(_ERROR_PIPE_CONNECTED) {
		return nil
	}
	return err
}

// ── Process attribute list helpers ─────────────────────────────

// allocateAttributeList builds a PROC_THREAD_ATTRIBUTE_LIST large enough
// for one attribute (the pseudo console) and initialises it.
// Returns the pointer and the buffer. The caller MUST keep the buffer
// alive (via runtime.KeepAlive) until after CreateProcessW.
func allocateAttributeList() (uintptr, []byte, error) {
	var size uintptr
	// First call with nil to get the required size.
	// SIZE_T is pointer-sized (8 bytes on x64).
	procInitializeProcThreadAttributeList.Call(
		0,
		1, // dwAttributeCount
		0,
		uintptr(unsafe.Pointer(&size)),
	)

	buf := make([]byte, int(size))

	r, _, err := procInitializeProcThreadAttributeList.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		1,
		0,
		uintptr(unsafe.Pointer(&size)),
	)
	if r == 0 {
		return 0, nil, fmt.Errorf("InitializeProcThreadAttributeList: %w", err)
	}

	return uintptr(unsafe.Pointer(&buf[0])), buf, nil
}

func updateProcThreadAttribute(attrListPtr uintptr, hpc _HPCON) error {
	r, _, err := procUpdateProcThreadAttribute.Call(
		attrListPtr,
		0, // dwFlags
		_PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		uintptr(unsafe.Pointer(&hpc)),
		unsafe.Sizeof(hpc),
		0,
		0,
	)
	if r == 0 {
		return fmt.Errorf("UpdateProcThreadAttribute: %w", err)
	}
	return nil
}

func deleteProcThreadAttributeList(attrListPtr uintptr) {
	procDeleteProcThreadAttributeList.Call(attrListPtr)
}

func buildWindowsCommandLine(command string, args []string) string {
	if command == "cmd" && len(args) == 0 {
		return "cmd /K"
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, syscall.EscapeArg(command))
	for _, arg := range args {
		parts = append(parts, syscall.EscapeArg(arg))
	}
	return strings.Join(parts, " ")
}

// ── SpawnPTY ───────────────────────────────────────────────────

// SpawnPTY starts a process attached to a Windows Pseudo Console (ConPTY).
// Fallback chain: node-pty sidecar → Go ConPTY → console → pipe.
func (m *Manager) SpawnPTY(command string, args []string, cwd string, cols, rows int, cfg *SpawnConfig) (types.SessionID, error) {
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

	if os.Getenv("SESSIONBRIDGE_DISABLE_NODE_PTY") != "1" && shouldUseNodePTY(command) {
		sid, err := m.spawnWithNodePTY(command, args, cwd, cols, rows, cfg)
		if err == nil {
			return sid, nil
		}
		log.Printf("[process] node-pty sidecar unavailable, trying Go ConPTY: %v", err)
	}

	// Phase 1: try ConPTY (works on Win10 1809+ with functioning conhost).
	driver, err := createConPTY(cols, rows)
	if err != nil {
		log.Printf("[process] ConPTY unavailable, trying console mode: %v", err)
		return m.spawnWithConsole(command, args, cwd, cols, rows, cfg)
	}

	sid, err := m.spawnWithConPTY(driver, command, args, cwd, cfg)
	if err != nil {
		log.Printf("[process] ConPTY spawn failed, trying console mode: %v", err)
		driver.Close()
		return m.spawnWithConsole(command, args, cwd, cols, rows, cfg)
	}
	return sid, nil
}

// spawnWithConPTY performs the full ConPTY process creation sequence.
// On failure the caller is responsible for cleaning up the driver.
func (m *Manager) spawnWithConPTY(driver *conPTYDriver, command string, args []string, cwd string, cfg *SpawnConfig) (types.SessionID, error) {
	procSetConsoleCtrlHandler.Call(0, 0)

	cmdLine := buildWindowsCommandLine(command, args)
	cmdLinePtr, err := syscall.UTF16PtrFromString(cmdLine)
	if err != nil {
		return "", fmt.Errorf("utf16 command line: %w", err)
	}

	// Build current directory pointer.
	var cwdPtr *uint16
	if cwd != "" {
		cwdPtr, err = syscall.UTF16PtrFromString(cwd)
		if err != nil {
			return "", fmt.Errorf("utf16 cwd: %w", err)
		}
	}

	// Set up STARTUPINFOEX with the pseudo console attribute.
	attrListPtr, attrListBuf, err := allocateAttributeList()
	if err != nil {
		return "", fmt.Errorf("alloc attr list: %w", err)
	}
	defer deleteProcThreadAttributeList(attrListPtr)

	if err := updateProcThreadAttribute(attrListPtr, driver.hpc); err != nil {
		return "", fmt.Errorf("update attr: %w", err)
	}

	si := &_STARTUPINFOEX{
		lpAttributeList: attrListPtr,
	}
	si.StartupInfo.Cb = uint32(unsafe.Sizeof(*si))
	si.StartupInfo.Flags = _STARTF_USESTDHANDLES

	readCh := driver.startOutputReader()

	// Create the process.
	pi := &_PROCESS_INFORMATION{}
	r, _, err := procCreateProcessW.Call(
		0,                                   // lpApplicationName
		uintptr(unsafe.Pointer(cmdLinePtr)), // lpCommandLine (mutable)
		0,                                   // lpProcessAttributes
		0,                                   // lpThreadAttributes
		0,                                   // bInheritHandles
		_EXTENDED_STARTUPINFO_PRESENT|_CREATE_UNICODE_ENVIRONMENT|_CREATE_NO_WINDOW,
		0,                               // lpEnvironment
		uintptr(unsafe.Pointer(cwdPtr)), // lpCurrentDirectory
		uintptr(unsafe.Pointer(si)),     // lpStartupInfo (=STARTUPINFOEX)
		uintptr(unsafe.Pointer(pi)),     // lpProcessInformation
	)
	// Keep attribute list buffer alive until the OS has consumed it.
	runtime.KeepAlive(attrListBuf)

	if r == 0 {
		return "", fmt.Errorf("CreateProcessW: %w", err)
	}

	// We no longer need the thread handle.
	syscall.CloseHandle(pi.Thread)
	driver.releaseConPTYSide()

	// Check if ConPTY process exits immediately with a crash code
	// (e.g. STATUS_DLL_INIT_FAILED on Windows Insider builds).
	// Exit code 0 is normal for short-lived commands and is NOT a failure.
	if status, _ := syscall.WaitForSingleObject(pi.Process, 200); status == syscall.WAIT_OBJECT_0 {
		var exitCode uint32
		syscall.GetExitCodeProcess(pi.Process, &exitCode)
		if exitCode != 0 {
			syscall.CloseHandle(pi.Process)
			return "", fmt.Errorf("ConPTY process crashed on start (exitCode=%d)", exitCode)
		}
		// exitCode == 0: process completed normally (e.g. cmd /C echo).
		// ConPTY output may still be readable from the pipe.
	}

	now := time.Now()
	sid := types.SessionID(fmt.Sprintf("sess_pty_%d_%d", pi.ProcessID, now.UnixMilli()))

	// Resolve tree metadata.
	parentSID := types.SessionID("")
	rootSID := types.SessionID("")
	pluginID := types.PluginID("")
	kind := ""
	if cfg != nil {
		parentSID = cfg.ParentSessionID
		pluginID = cfg.PluginID
		kind = cfg.Kind
	}
	if parentSID != "" {
		m.mu.Lock()
		if parent := m.processes[parentSID]; parent != nil {
			if parent.RootSessionID != "" {
				rootSID = parent.RootSessionID
			} else {
				rootSID = parentSID
			}
		}
		m.mu.Unlock()
	}

	proc := &Process{
		SessionID:       sid,
		ParentSessionID: parentSID,
		RootSessionID:   rootSID,
		PluginID:        pluginID,
		Kind:            kind,
		Cmd:             nil, // ConPTY processes don't use exec.Cmd
		State:           "running",
		CreatedAt:       now.UnixMilli(),
		PID:             int(pi.ProcessID),
		ptyDriver:       driver,
		processHandle:   uintptr(pi.Process),
	}

	m.mu.Lock()
	m.processes[sid] = proc
	m.mu.Unlock()

	if m.onSpawn != nil {
		m.onSpawn(sid)
	}

	m.pushEvent(sid, "started", map[string]interface{}{"pid": proc.PID})

	// Forward ConPTY output. The reader is started before CreateProcessW to
	// match node-pty's worker-first conout connection order.
	go func() {
		for result := range readCh {
			if len(result.data) > 0 {
				seq := types.EventSeq(m.seq.Add(1))
				m.pusher(sid, "stdout", seq, string(result.data))
			}
			if result.err != nil {
				if result.err == syscall.ERROR_BROKEN_PIPE || result.err == syscall.Errno(6) /* ERROR_INVALID_HANDLE */ || result.err == syscall.Errno(995) /* ERROR_OPERATION_ABORTED */ {
					return // normal EOF or cleanup closed the handle
				}
				if result.err != io.EOF {
					log.Printf("[process] conpty session %s read error: %v", sid, result.err)
				}
				return
			}
		}
	}()

	// Wait for the process to exit.
	go func() {
		const INFINITE = 0xFFFFFFFF
		syscall.WaitForSingleObject(pi.Process, INFINITE)

		var exitCode uint32
		syscall.GetExitCodeProcess(pi.Process, &exitCode)
		syscall.CloseHandle(pi.Process)

		// Close ConPTY handle so the output pipe gets released,
		// unblocking the read goroutine.
		if driver.hpc != 0 {
			procClosePseudoConsole.Call(uintptr(driver.hpc))
			driver.hpc = 0
		}

		m.mu.Lock()
		if p, ok := m.processes[sid]; ok {
			p.State = "exited"
			p.ExitCode = int(exitCode)
		}
		m.mu.Unlock()

		m.pushEvent(sid, "exited", map[string]interface{}{"exitCode": int(exitCode)})
	}()

	return sid, nil
}

// terminateByHandle sends a kill signal via the OS process handle.
// Used by signalProcess when exec.Cmd is not available (ConPTY).
func terminateByHandle(h uintptr) error {
	return syscall.TerminateProcess(syscall.Handle(h), 1)
}

func (d *conPTYDriver) releaseConPTYSide() {
	if usingBundledConPTY && procReleasePseudoConsole != nil && d.hpc != 0 {
		procReleasePseudoConsole.Call(uintptr(d.hpc))
	}
	if d.conptyInput != 0 {
		syscall.CloseHandle(d.conptyInput)
		d.conptyInput = 0
	}
	if d.conptyOutput != 0 {
		syscall.CloseHandle(d.conptyOutput)
		d.conptyOutput = 0
	}
}

// spawnWithConsole spawns a process with a real (hidden) Windows console
// and scrapes the screen buffer for output. This is the fallback for systems
// where ConPTY is broken (e.g. Insider builds).
func (m *Manager) spawnWithConsole(command string, args []string, cwd string, cols, rows int, cfg *SpawnConfig) (types.SessionID, error) {
	driver, pi, err := createConsoleProcess(command, args, cwd, cols, rows)
	if err != nil {
		log.Printf("[process] console mode failed, falling back to pipe: %v", err)
		return m.Spawn(command, args, cwd, cfg)
	}

	// Close the thread handle — we only need the process handle.
	syscall.CloseHandle(syscall.Handle(pi.Thread))

	now := time.Now()
	sid := types.SessionID(fmt.Sprintf("sess_pty_%d_%d", pi.ProcessID, now.UnixMilli()))

	parentSID := types.SessionID("")
	rootSID := types.SessionID("")
	pluginID := types.PluginID("")
	kind := ""
	if cfg != nil {
		parentSID = cfg.ParentSessionID
		pluginID = cfg.PluginID
		kind = cfg.Kind
	}
	if parentSID != "" {
		m.mu.Lock()
		if parent := m.processes[parentSID]; parent != nil {
			if parent.RootSessionID != "" {
				rootSID = parent.RootSessionID
			} else {
				rootSID = parentSID
			}
		}
		m.mu.Unlock()
	}

	proc := &Process{
		SessionID:       sid,
		ParentSessionID: parentSID,
		RootSessionID:   rootSID,
		PluginID:        pluginID,
		Kind:            kind,
		Cmd:             nil, // console-mode processes don't use exec.Cmd
		State:           "running",
		CreatedAt:       now.UnixMilli(),
		PID:             int(pi.ProcessID),
		ptyDriver:       driver,
		processHandle:   uintptr(pi.Process),
	}

	m.mu.Lock()
	m.processes[sid] = proc
	m.mu.Unlock()

	if m.onSpawn != nil {
		m.onSpawn(sid)
	}

	m.pushEvent(sid, "started", map[string]interface{}{"pid": proc.PID})

	// Output reading goroutine: poll the screen buffer and push changes.
	go func() {
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				output := driver.readScreen()
				if output != "" {
					seq := types.EventSeq(m.seq.Add(1))
					m.pusher(sid, "stdout", seq, output)
				}
			case <-driver.stopCh:
				// Drain any remaining output before exiting.
				output := driver.readScreen()
				if output != "" {
					seq := types.EventSeq(m.seq.Add(1))
					m.pusher(sid, "stdout", seq, output)
				}
				return
			}
		}
	}()

	// Process exit watcher.
	go func() {
		const INFINITE = 0xFFFFFFFF
		syscall.WaitForSingleObject(syscall.Handle(pi.Process), INFINITE)

		var exitCode uint32
		syscall.GetExitCodeProcess(syscall.Handle(pi.Process), &exitCode)
		syscall.CloseHandle(syscall.Handle(pi.Process))

		// Stop the polling goroutine and cleanup.
		driver.Close()

		m.mu.Lock()
		if p, ok := m.processes[sid]; ok {
			p.State = "exited"
			p.ExitCode = int(exitCode)
		}
		m.mu.Unlock()

		m.pushEvent(sid, "exited", map[string]interface{}{"exitCode": int(exitCode)})
	}()

	return sid, nil
}
