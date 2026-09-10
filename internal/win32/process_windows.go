//go:build windows && amd64

package win32

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	domain "github.com/fcying/CommandTrayHostGo/internal/app"
	"github.com/fcying/CommandTrayHostGo/internal/config"
	"golang.org/x/sys/windows"
)

const (
	seeMaskNoCloseProcess = 0x00000040
	gwOwner               = 4
	wsExToolWindow        = 0x00000080
	startfUseSize         = 0x00000002
	startfUsePosition     = 0x00000004
)

var (
	procAttachConsole            = kernel32.NewProc("AttachConsole")
	procFreeConsole              = kernel32.NewProc("FreeConsole")
	procGetConsoleWindow         = kernel32.NewProc("GetConsoleWindow")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindow                = user32.NewProc("GetWindow")
	procGetWindowLongPtrW        = user32.NewProc("GetWindowLongPtrW")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	procShellExecuteW            = shell32.NewProc("ShellExecuteW")
	procShellExecuteExW          = shell32.NewProc("ShellExecuteExW")
	closeWindowsCallback         = windows.NewCallback(closeWindowsProc)
	findWindowCallback           = windows.NewCallback(findWindowProc)
	windowSearchMu               sync.Mutex
	windowSearches               = make(map[uintptr]*windowSearch)
	nextWindowSearchID           uintptr
)

type windowSearch struct {
	pids       map[uint32]struct{}
	candidates map[uint32]windowCandidate
}

type windowCandidate struct {
	visible         uintptr
	hidden          uintptr
	fallbackVisible uintptr
	fallbackHidden  uintptr
}

type processController struct {
	baseDir        string
	executablePath string
	job            windows.Handle
}

type childProcess struct {
	handle     windows.Handle
	helperPath string
	pid        uint32
}

type shellExecuteInfo struct {
	Size       uint32
	Mask       uint32
	HWND       uintptr
	Verb       *uint16
	File       *uint16
	Parameters *uint16
	Directory  *uint16
	Show       int32
	Instance   uintptr
	IDList     uintptr
	Class      *uint16
	ClassKey   windows.Handle
	HotKey     uint32
	Icon       uintptr
	Process    windows.Handle
}

func newProcessController(baseDir, executablePath string) (*processController, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("CreateJobObjectW: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("SetInformationJobObject: %w", err)
	}
	return &processController{baseDir: baseDir, executablePath: executablePath, job: job}, nil
}

func SystemDirectory() (string, error) {
	directory, err := windows.GetSystemDirectory()
	if err != nil {
		return "", fmt.Errorf("GetSystemDirectoryW: %w", err)
	}
	return filepath.ToSlash(directory), nil
}

func (c *processController) Close() {
	if c == nil || c.job == 0 {
		return
	}
	windows.CloseHandle(c.job)
	c.job = 0
}

func (c *processController) Start(entry config.EntryConfig, ownership domain.Ownership, startShowSilent, startVisible bool, windowTitle string) (*childProcess, error) {
	executable, parameters, workingDirectory, err := c.resolveCommand(entry)
	if err != nil {
		return nil, err
	}
	if err := validateExecutable(executable); err != nil {
		return nil, fmt.Errorf("%s: %w", entry.Name, err)
	}
	if entry.RequireAdmin && !IsElevated() {
		if ownership != domain.FullyDetached {
			return nil, fmt.Errorf("%s requires administrator rights; elevate CommandTrayHost to keep the process in its job object", entry.Name)
		}
		return c.startElevated(entry, executable, parameters, workingDirectory)
	}

	commandLine := windows.EscapeArg(executable)
	if parameters != "" {
		commandLine += " " + parameters
	}
	commandLineUTF16, err := windows.UTF16FromString(commandLine)
	if err != nil {
		return nil, fmt.Errorf("%s command line: %w", entry.Name, err)
	}
	workingDirectoryPtr, err := windows.UTF16PtrFromString(workingDirectory)
	if err != nil {
		return nil, fmt.Errorf("%s working directory: %w", entry.Name, err)
	}
	title, err := windows.UTF16PtrFromString(windowTitle)
	if err != nil {
		return nil, fmt.Errorf("%s title: %w", entry.Name, err)
	}
	showCommand := uint16(windows.SW_HIDE)
	if startVisible && (ownership != domain.ManagedAndJobOwned || !startShowSilent) {
		showCommand = windows.SW_SHOW
	}
	startup := windows.StartupInfo{
		Cb:         uint32(unsafe.Sizeof(windows.StartupInfo{})),
		Title:      title,
		Flags:      windows.STARTF_USESHOWWINDOW,
		ShowWindow: showCommand,
	}
	if !startShowSilent || ownership != domain.ManagedAndJobOwned {
		screenWidth, _, _ := procGetSystemMetrics.Call(smCXFullScreen)
		screenHeight, _, _ := procGetSystemMetrics.Call(smCYFullScreen)
		if x, y, ok := entry.PositionPixels(int32(screenWidth), int32(screenHeight)); ok {
			startup.Flags |= startfUsePosition
			startup.X = uint32(x)
			startup.Y = uint32(y)
		}
		if width, height, ok := entry.SizePixels(int32(screenWidth), int32(screenHeight)); ok {
			startup.Flags |= startfUseSize
			startup.XSize = uint32(width)
			startup.YSize = uint32(height)
		}
	}
	var processInfo windows.ProcessInformation
	err = windows.CreateProcess(
		nil,
		&commandLineUTF16[0],
		nil,
		nil,
		false,
		windows.CREATE_NEW_CONSOLE|windows.CREATE_BREAKAWAY_FROM_JOB|windows.CREATE_SUSPENDED,
		nil,
		workingDirectoryPtr,
		&startup,
		&processInfo,
	)
	runtime.KeepAlive(commandLineUTF16)
	runtime.KeepAlive(workingDirectoryPtr)
	runtime.KeepAlive(title)
	if err != nil {
		if errors.Is(err, windows.ERROR_ELEVATION_REQUIRED) {
			if ownership != domain.FullyDetached {
				return nil, fmt.Errorf("%s requires administrator rights; elevate CommandTrayHost to keep the process in its job object", entry.Name)
			}
			return c.startElevated(entry, executable, parameters, workingDirectory)
		}
		return nil, fmt.Errorf("start %s: %w", entry.Name, err)
	}
	return c.finishStart(entry.Name, ownership, processInfo.Process, processInfo.Thread, processInfo.ProcessId)
}

func (c *processController) StartConsoleFallback(title string) (*childProcess, error) {
	systemDirectory, err := windows.GetSystemDirectory()
	if err != nil {
		return nil, fmt.Errorf("GetSystemDirectoryW: %w", err)
	}
	executable := filepath.Join(systemDirectory, "cmd.exe")
	commandLine, err := windows.UTF16FromString(windows.EscapeArg(executable) + " /d /v:on /k title " + title)
	if err != nil {
		return nil, err
	}
	workingDirectory, err := windows.UTF16PtrFromString(c.baseDir)
	if err != nil {
		return nil, err
	}
	titlePtr, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return nil, err
	}
	startup := windows.StartupInfo{
		Cb:         uint32(unsafe.Sizeof(windows.StartupInfo{})),
		Title:      titlePtr,
		Flags:      windows.STARTF_USESHOWWINDOW,
		ShowWindow: windows.SW_HIDE,
	}
	var processInfo windows.ProcessInformation
	err = windows.CreateProcess(
		nil,
		&commandLine[0],
		nil,
		nil,
		false,
		windows.CREATE_NEW_CONSOLE|windows.CREATE_BREAKAWAY_FROM_JOB|windows.CREATE_SUSPENDED,
		nil,
		workingDirectory,
		&startup,
		&processInfo,
	)
	runtime.KeepAlive(commandLine)
	runtime.KeepAlive(workingDirectory)
	runtime.KeepAlive(titlePtr)
	if err != nil {
		return nil, fmt.Errorf("start CommandTrayHost console: %w", err)
	}
	return c.finishStart("CommandTrayHost console", domain.ManagedAndJobOwned, processInfo.Process, processInfo.Thread, processInfo.ProcessId)
}

func (c *processController) startElevated(entry config.EntryConfig, executable, parameters, workingDirectory string) (*childProcess, error) {
	show := int32(windows.SW_HIDE)
	if entry.EffectiveStartShow() {
		show = windows.SW_SHOW
	}
	handle, err := shellExecuteProcess(executable, parameters, workingDirectory, show)
	if err != nil {
		return nil, fmt.Errorf("run %s as administrator: %w", entry.Name, err)
	}
	windows.CloseHandle(handle)
	return nil, nil
}

func (c *processController) finishStart(name string, ownership domain.Ownership, handle, thread windows.Handle, pid uint32) (*childProcess, error) {
	closeStartedProcess := func() {
		_ = windows.TerminateProcess(handle, 1)
		_, _ = windows.WaitForSingleObject(handle, 5000)
		windows.CloseHandle(thread)
		windows.CloseHandle(handle)
	}
	if ownership != domain.FullyDetached {
		if err := windows.AssignProcessToJobObject(c.job, handle); err != nil {
			closeStartedProcess()
			return nil, fmt.Errorf("assign %s to job object: %w", name, err)
		}
	}
	if _, err := windows.ResumeThread(thread); err != nil {
		closeStartedProcess()
		return nil, fmt.Errorf("resume %s: %w", name, err)
	}
	windows.CloseHandle(thread)
	if ownership != domain.ManagedAndJobOwned {
		windows.CloseHandle(handle)
		return nil, nil
	}
	return &childProcess{handle: handle, pid: pid, helperPath: c.executablePath}, nil
}

func (c *processController) resolveCommand(entry config.EntryConfig) (string, string, string, error) {
	executable, parameters, _, workingDirectory, err := resolveEntryCommand(c.baseDir, entry)
	return executable, parameters, workingDirectory, err
}

func resolveEntryCommand(baseDir string, entry config.EntryConfig) (string, string, string, string, error) {
	executable, parameters, err := domain.SplitExecutable(entry.Command)
	if err != nil {
		return "", "", "", "", fmt.Errorf("%s: %w", entry.Name, err)
	}
	path := entry.Path
	if filepath.IsAbs(path) {
		path = filepath.Clean(path)
	} else if path == "" && filepath.IsAbs(executable) {
		path = filepath.Dir(executable)
	} else {
		path = filepath.Join(baseDir, path)
	}
	if !filepath.IsAbs(executable) {
		executable = filepath.Join(path, executable)
	}

	workingDirectory := entry.WorkingDirectory
	switch {
	case filepath.IsAbs(workingDirectory):
		workingDirectory = filepath.Clean(workingDirectory)
	case strings.HasPrefix(workingDirectory, ">"):
		workingDirectory = filepath.Join(baseDir, strings.TrimPrefix(workingDirectory, ">"))
	case workingDirectory == "":
		workingDirectory = path
	default:
		workingDirectory = filepath.Join(path, workingDirectory)
	}
	return executable, parameters, path, workingDirectory, nil
}

func validateExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("executable %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("executable %s is a directory", path)
	}
	return nil
}

func shellOpen(hwnd uintptr, target, parameters, workingDirectory string, show int32) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	targetPtr, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	var parametersPtr, workingDirectoryPtr *uint16
	if parameters != "" {
		parametersPtr, err = windows.UTF16PtrFromString(parameters)
		if err != nil {
			return err
		}
	}
	if workingDirectory != "" {
		workingDirectoryPtr, err = windows.UTF16PtrFromString(workingDirectory)
		if err != nil {
			return err
		}
	}
	result, _, callErr := procShellExecuteW.Call(
		hwnd,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(targetPtr)),
		uintptr(unsafe.Pointer(parametersPtr)),
		uintptr(unsafe.Pointer(workingDirectoryPtr)),
		uintptr(show),
	)
	runtime.KeepAlive(verb)
	runtime.KeepAlive(targetPtr)
	runtime.KeepAlive(parametersPtr)
	runtime.KeepAlive(workingDirectoryPtr)
	if result <= 32 {
		return fmt.Errorf("ShellExecuteW(%q) returned %d: %w", target, result, callErr)
	}
	return nil
}

func selectInExplorer(hwnd uintptr, path string) error {
	return shellOpen(hwnd, "explorer.exe", "/select,"+windows.EscapeArg(path), filepath.Dir(path), windows.SW_SHOWNORMAL)
}

func (p *childProcess) Running() bool {
	running, err := p.running()
	return err == nil && running
}

func findWindows(pids map[uint32]struct{}) (map[uint32]uintptr, error) {
	search := windowSearch{
		pids:       pids,
		candidates: make(map[uint32]windowCandidate, len(pids)),
	}
	windowSearchMu.Lock()
	nextWindowSearchID++
	if nextWindowSearchID == 0 {
		nextWindowSearchID++
	}
	searchID := nextWindowSearchID
	windowSearches[searchID] = &search
	windowSearchMu.Unlock()
	ret, _, err := procEnumWindows.Call(findWindowCallback, searchID)
	windowSearchMu.Lock()
	delete(windowSearches, searchID)
	windowSearchMu.Unlock()
	if ret == 0 {
		if err == syscall.Errno(0) {
			err = errors.New("unknown failure")
		}
		return nil, fmt.Errorf("EnumWindows: %w", err)
	}
	windowsByPID := make(map[uint32]uintptr, len(search.candidates))
	for pid, candidate := range search.candidates {
		switch {
		case candidate.visible != 0:
			windowsByPID[pid] = candidate.visible
		case candidate.hidden != 0:
			windowsByPID[pid] = candidate.hidden
		case candidate.fallbackVisible != 0:
			windowsByPID[pid] = candidate.fallbackVisible
		case candidate.fallbackHidden != 0:
			windowsByPID[pid] = candidate.fallbackHidden
		}
	}
	return windowsByPID, nil
}

func (p *childProcess) ownsWindow(hwnd uintptr) bool {
	if p == nil || p.handle == 0 || hwnd == 0 || !isWindow(hwnd) {
		return false
	}
	var windowPID uint32
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&windowPID)))
	return windowPID == p.pid
}

func (p *childProcess) Stop(timeout uint32, killTree, isGUI bool) error {
	if p == nil || p.handle == 0 {
		return nil
	}
	running, err := p.running()
	if err != nil {
		return err
	}
	if !running {
		p.Close()
		return nil
	}
	if killTree {
		systemDirectory, err := windows.GetSystemDirectory()
		if err != nil {
			return fmt.Errorf("resolve System32: %w", err)
		}
		command := exec.Command(filepath.Join(systemDirectory, "taskkill.exe"), "/F", "/T", "/PID", fmt.Sprint(p.pid))
		command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		commandErr := command.Run()
		exited, waitErr := p.waitForExit(5000)
		if exited {
			p.Close()
			return nil
		}
		if commandErr != nil {
			return fmt.Errorf("taskkill PID %d: %w", p.pid, commandErr)
		}
		return fmt.Errorf("taskkill PID %d did not stop the process: %w", p.pid, waitErr)
	}
	if isGUI {
		enumCloseWindows(p.pid)
	}
	exited, err := p.waitForExit(timeout)
	if exited {
		p.Close()
		return nil
	}
	if err != nil {
		return err
	}
	if err := windows.TerminateProcess(p.handle, 0); err != nil && p.Running() {
		return fmt.Errorf("terminate PID %d: %w", p.pid, err)
	}
	exited, err = p.waitForExit(5000)
	if !exited {
		return fmt.Errorf("PID %d did not exit after TerminateProcess: %w", p.pid, err)
	}
	p.Close()
	return nil
}

func (p *childProcess) consoleWindow() uintptr {
	if p == nil || p.handle == 0 || p.helperPath == "" {
		return 0
	}
	running, err := p.running()
	if err != nil || !running {
		return 0
	}

	command := exec.Command(p.helperPath, domain.ConsoleWindowQueryArgument(p.pid))
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := command.Output()
	if err != nil || len(output) != 8 {
		return 0
	}
	running, err = p.running()
	if err != nil || !running {
		return 0
	}
	hwndValue := binary.LittleEndian.Uint64(output)
	hwnd := uintptr(hwndValue)
	if uint64(hwnd) != hwndValue || hwnd == 0 || !isWindow(hwnd) || windowClassName(hwnd) != "ConsoleWindowClass" {
		return 0
	}
	parent, _, _ := procGetParent.Call(hwnd)
	owner, _, _ := procGetWindow.Call(hwnd, gwOwner)
	if parent != 0 || owner != 0 {
		return 0
	}
	return hwnd
}

func QueryConsoleWindow(pid uint32) uintptr {
	if pid == 0 {
		return 0
	}
	attached, _, _ := procAttachConsole.Call(uintptr(pid))
	if attached == 0 {
		return 0
	}
	defer procFreeConsole.Call()
	hwnd, _, _ := procGetConsoleWindow.Call()
	return hwnd
}

func (p *childProcess) running() (bool, error) {
	if p == nil || p.handle == 0 {
		return false, nil
	}
	result, err := windows.WaitForSingleObject(p.handle, 0)
	switch result {
	case windows.WAIT_OBJECT_0:
		return false, nil
	case uint32(windows.WAIT_TIMEOUT):
		return true, nil
	default:
		return false, fmt.Errorf("wait for PID %d: %w", p.pid, err)
	}
}

func (p *childProcess) waitForExit(timeout uint32) (bool, error) {
	result, err := windows.WaitForSingleObject(p.handle, timeout)
	switch result {
	case windows.WAIT_OBJECT_0:
		return true, nil
	case uint32(windows.WAIT_TIMEOUT):
		return false, nil
	default:
		return false, fmt.Errorf("wait for PID %d: %w", p.pid, err)
	}
}

func (p *childProcess) Close() {
	if p == nil || p.handle == 0 {
		return
	}
	windows.CloseHandle(p.handle)
	p.handle = 0
}

func enumCloseWindows(pid uint32) {
	procEnumWindows.Call(closeWindowsCallback, uintptr(pid))
}

func closeWindowsProc(hwnd, targetPID uintptr) uintptr {
	var windowPID uint32
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&windowPID)))
	if windowPID == uint32(targetPID) {
		procPostMessageW.Call(hwnd, wmClose, 0, 0)
	}
	return 1
}

func preferWindowCandidate(hwnd, current uintptr) bool {
	if current == 0 {
		return true
	}
	return windowCandidateArea(hwnd) > windowCandidateArea(current)
}

func windowCandidateArea(hwnd uintptr) int64 {
	var rect windows.Rect
	ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
	if ret == 0 {
		return 0
	}
	width := int64(rect.Right) - int64(rect.Left)
	height := int64(rect.Bottom) - int64(rect.Top)
	if width <= 0 || height <= 0 {
		return 0
	}
	return width * height
}

func findWindowProc(hwnd, lparam uintptr) uintptr {
	windowSearchMu.Lock()
	search := windowSearches[lparam]
	windowSearchMu.Unlock()
	if search == nil {
		return 0
	}
	var windowPID uint32
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&windowPID)))
	if _, ok := search.pids[windowPID]; !ok {
		return 1
	}
	owner, _, _ := procGetWindow.Call(hwnd, gwOwner)
	exStyle, err := getWindowLongPtr(hwnd, ^uintptr(19))
	if err != nil {
		return 1
	}
	isFallback := owner != 0 || exStyle&wsExToolWindow != 0
	candidate := search.candidates[windowPID]
	if isWindowVisible(hwnd) {
		if isFallback {
			if preferWindowCandidate(hwnd, candidate.fallbackVisible) {
				candidate.fallbackVisible = hwnd
			}
		} else if preferWindowCandidate(hwnd, candidate.visible) {
			candidate.visible = hwnd
		}
	} else if isFallback {
		if preferWindowCandidate(hwnd, candidate.fallbackHidden) {
			candidate.fallbackHidden = hwnd
		}
	} else if preferWindowCandidate(hwnd, candidate.hidden) {
		candidate.hidden = hwnd
	}
	search.candidates[windowPID] = candidate
	return 1
}

func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func RelaunchElevated(executablePath, workingDirectory, startupUserSID, configPath string) error {
	parameters := "force-restart"
	if startupUserSID != "" {
		parameters += " startup-user=" + windows.EscapeArg(startupUserSID)
	}
	if configPath != "" {
		parameters += " -c " + windows.EscapeArg(configPath)
	}
	handle, err := shellExecuteProcess(executablePath, parameters, workingDirectory, windows.SW_HIDE)
	if err != nil {
		return fmt.Errorf("relaunch as administrator: %w", err)
	}
	windows.CloseHandle(handle)
	return nil
}

func shellExecuteProcess(executable, parameters, workingDirectory string, show int32) (windows.Handle, error) {
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return 0, err
	}
	directory, err := windows.UTF16PtrFromString(workingDirectory)
	if err != nil {
		return 0, err
	}
	var parameterPtr *uint16
	if parameters != "" {
		parameterPtr, err = windows.UTF16PtrFromString(parameters)
		if err != nil {
			return 0, err
		}
	}
	info := shellExecuteInfo{
		Size:       uint32(unsafe.Sizeof(shellExecuteInfo{})),
		Mask:       seeMaskNoCloseProcess,
		Verb:       verb,
		File:       file,
		Parameters: parameterPtr,
		Directory:  directory,
		Show:       show,
	}
	ret, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(verb)
	runtime.KeepAlive(file)
	runtime.KeepAlive(parameterPtr)
	runtime.KeepAlive(directory)
	if ret == 0 {
		return 0, callErr
	}
	if info.Process == 0 {
		return 0, errors.New("ShellExecuteExW returned no process handle")
	}
	return info.Process, nil
}
