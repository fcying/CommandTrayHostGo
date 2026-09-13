//go:build windows && amd64

package win32

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/fcying/CommandTrayHostGo/internal/config"
	"golang.org/x/sys/windows"
)

// Runtime-only booleans keep even maximum-size configurations compact.
const elevationStateLimit = config.MaxConfigEntries*128 + 2*32768*6 + 4096
const elevationReadyTimeout = 20 * time.Second
const elevationPipePrefix = `\\.\pipe\CommandTrayHost.Elevation.`
const (
	elevationInitial byte = iota + 1
	elevationReady
	elevationFinal
	elevationFinalReady
	elevationCommit
)

type ElevationState struct {
	Entries            []ElevationEntry
	SourceConfigDigest [32]byte
	Config             config.Config    `json:"-"`
	ConfigStamp        config.FileStamp `json:"-"`
}
type ElevationEntry struct {
	Enabled       bool
	Show          bool
	Launched      bool
	CronTransient bool
}
type elevationEnvelope struct {
	SourcePID      uint32
	Target         string
	ConfigPath     string
	State          ElevationState
	TargetElevated bool
}

func elevationIdentity(target, argument string) (string, string, error) {
	target, err := filepath.Abs(target)
	if err != nil {
		return "", "", err
	}
	if argument == "" {
		argument = "config.json"
	}
	if !filepath.IsAbs(argument) {
		argument = filepath.Join(filepath.Dir(target), argument)
	}
	return filepath.Clean(target), filepath.Clean(argument), nil
}

func createElevationPipe(originalSID string) (windows.Handle, string, error) {
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return 0, "", err
	}
	name := elevationPipePrefix + strconv.Itoa(os.Getpid()) + "." + hex.EncodeToString(nonce[:])
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return 0, "", err
	}
	if originalSID == "" {
		originalSID = user.User.Sid.String()
	}
	if _, err := windows.StringToSid(originalSID); err != nil {
		return 0, "", err
	}
	// Explicit original-user access and a medium label permit high-to-medium
	// handoff. The exact kernel client PID remains the authorization boundary.
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + user.User.Sid.String() + ")(A;;GA;;;" + originalSID + ")(A;;GA;;;BA)S:(ML;;NW;;;ME)")
	if err != nil {
		return 0, "", err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, "", err
	}
	h, err := windows.CreateNamedPipe(p, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE, windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 65536, 65536, 0, &sa)
	return h, name, err
}

func elevationPipeSource(name string) (uint32, error) {
	if !strings.HasPrefix(name, elevationPipePrefix) {
		return 0, errors.New("invalid elevation pipe name")
	}
	parts := strings.Split(strings.TrimPrefix(name, elevationPipePrefix), ".")
	if len(parts) != 2 || len(parts[1]) != 64 {
		return 0, errors.New("invalid elevation pipe name")
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return 0, err
	}
	pid, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil || pid == 0 {
		return 0, errors.New("invalid elevation source PID")
	}
	return uint32(pid), nil
}

func pipeProcessID(pipe windows.Handle, server bool) (uint32, error) {
	var pid uint32
	var err error
	if server {
		err = windows.GetNamedPipeServerProcessId(pipe, &pid)
	} else {
		err = windows.GetNamedPipeClientProcessId(pipe, &pid)
	}
	return pid, err
}

// Every outstanding operation owns its event and is cancelled AND drained before
// its OVERLAPPED or buffer can leave scope, including timeout/error paths.
func elevationIO(pipe windows.Handle, deadline time.Time, start func(*windows.Overlapped) error) (uint32, error) {
	if !time.Now().Before(deadline) {
		return 0, errors.New("elevation pipe timed out")
	}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(event)
	ov := windows.Overlapped{HEvent: event}
	err = start(&ov)
	if errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		return 0, nil
	}
	if err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) {
		return 0, err
	}
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		remaining := time.Until(deadline)
		var status uint32
		if remaining > 0 {
			status, err = windows.WaitForSingleObject(event, uint32((remaining+time.Millisecond-1)/time.Millisecond))
		} else {
			status = uint32(windows.WAIT_TIMEOUT)
		}
		if err != nil || status != windows.WAIT_OBJECT_0 {
			_ = windows.CancelIoEx(pipe, &ov)
			var drained uint32
			_ = windows.GetOverlappedResult(pipe, &ov, &drained, true)
			if err != nil {
				return 0, err
			}
			return 0, errors.New("elevation pipe timed out")
		}
	}
	var transferred uint32
	err = windows.GetOverlappedResult(pipe, &ov, &transferred, false)
	return transferred, err
}

func elevationBytes(pipe windows.Handle, data []byte, write bool, deadline time.Time) error {
	for len(data) > 0 {
		var immediate uint32
		n, err := elevationIO(pipe, deadline, func(ov *windows.Overlapped) error {
			if write {
				return windows.WriteFile(pipe, data, &immediate, ov)
			}
			return windows.ReadFile(pipe, data, &immediate, ov)
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
		data = data[n:]
	}
	return nil
}

func sendElevationFrame(pipe windows.Handle, kind byte, data []byte, deadline time.Time) error {
	if len(data) > elevationStateLimit {
		return errors.New("elevation state exceeds size limit")
	}
	header := [5]byte{kind}
	binary.LittleEndian.PutUint32(header[1:], uint32(len(data)))
	if err := elevationBytes(pipe, header[:], true, deadline); err != nil {
		return err
	}
	return elevationBytes(pipe, data, true, deadline)
}

func readElevationFrame(pipe windows.Handle, kind byte, deadline time.Time) ([]byte, error) {
	var header [5]byte
	if err := elevationBytes(pipe, header[:], false, deadline); err != nil {
		return nil, err
	}
	size := binary.LittleEndian.Uint32(header[1:])
	if header[0] != kind || size > elevationStateLimit || (kind != elevationInitial && kind != elevationFinal && size != 0) {
		return nil, errors.New("invalid elevation frame")
	}
	data := make([]byte, int(size))
	if err := elevationBytes(pipe, data, false, deadline); err != nil {
		return nil, err
	}
	return data, nil
}

func acceptElevationReceiver(pipe windows.Handle, receiverPID uint32, deadline time.Time) error {
	for time.Now().Before(deadline) {
		_, err := elevationIO(pipe, deadline, func(ov *windows.Overlapped) error { return windows.ConnectNamedPipe(pipe, ov) })
		if err != nil {
			return err
		}
		pid, err := pipeProcessID(pipe, false)
		if err == nil && pid == receiverPID {
			return nil
		}
		// Never transmit even the initial snapshot to an unauthenticated client.
		if err := windows.DisconnectNamedPipe(pipe); err != nil {
			return err
		}
	}
	return errors.New("timed out authenticating elevation receiver")
}

// authorizeElevationSource grants only read-only identity and exit-wait access
// to the original user. A medium receiver under a different account otherwise
// cannot open the elevated source process. The original DACL is restored once
// READY proves the receiver owns its handle, and on every failed handoff.
func authorizeElevationSource(originalSID string) (func() error, error) {
	sid, err := windows.StringToSid(originalSID)
	if err != nil {
		return nil, err
	}
	process := windows.CurrentProcess()
	sd, err := windows.GetSecurityInfo(process, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	original, _, err := sd.DACL()
	if err != nil {
		return nil, err
	}
	if original == nil {
		return nil, errors.New("elevation source has an unrestricted process DACL")
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.SYNCHRONIZE | windows.PROCESS_QUERY_LIMITED_INFORMATION,
		AccessMode:        windows.GRANT_ACCESS,
		Trustee:           windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER, TrusteeValue: windows.TrusteeValueFromSID(sid)},
	}}, original)
	runtime.KeepAlive(sid)
	if err != nil {
		return nil, err
	}
	if err := windows.SetSecurityInfo(process, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		return nil, err
	}
	restored := false
	return func() error {
		if restored {
			return nil
		}
		err := windows.SetSecurityInfo(process, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, original, nil)
		runtime.KeepAlive(sd)
		if err == nil {
			restored = true
		}
		return err
	}, nil
}

// BeginElevation commits only after both snapshots have been acknowledged.
// UAC runs before bounded pipe operations, so cancellation leaves no pending I/O.
func BeginElevation(target, configArgument, startupUserSID string, initial ElevationState, refresh func() (ElevationState, bool, error)) (result error) {
	target, configPath, err := elevationIdentity(target, configArgument)
	if err != nil {
		return err
	}
	targetElevated := !IsElevated()
	if startupUserSID == "" {
		user, err := windows.GetCurrentProcessToken().GetTokenUser()
		if err != nil {
			return err
		}
		startupUserSID = user.User.Sid.String()
	}
	envelope := elevationEnvelope{SourcePID: uint32(os.Getpid()), Target: target, ConfigPath: configPath, State: initial, TargetElevated: targetElevated}
	if _, err := encodeElevationEnvelope(envelope); err != nil {
		return err
	}
	pipe, name, err := createElevationPipe(startupUserSID)
	if err != nil {
		return fmt.Errorf("create elevation pipe: %w", err)
	}
	defer windows.CloseHandle(pipe)
	parameters := "force-restart"
	parameters += " startup-user=" + windows.EscapeArg(startupUserSID)
	if configArgument != "" {
		parameters += " -c " + windows.EscapeArg(configArgument)
	}
	parameters += " --elevation-pipe " + windows.EscapeArg(name)
	var restore func() error
	if !targetElevated {
		restore, err = authorizeElevationSource(startupUserSID)
		if err != nil {
			return fmt.Errorf("authorize elevation source: %w", err)
		}
		defer func() {
			if err := restore(); err != nil {
				result = errors.Join(result, fmt.Errorf("restore elevation source access: %w", err))
			}
		}()
	}
	var receiver windows.Handle
	if targetElevated {
		receiver, err = shellExecuteProcess(target, parameters, filepath.Dir(target), windows.SW_HIDE)
	} else {
		receiver, err = launchUnelevated(target, parameters, filepath.Dir(target), startupUserSID)
	}
	if err != nil {
		return fmt.Errorf("relaunch with toggled privilege: %w", err)
	}
	defer windows.CloseHandle(receiver)
	pid, err := windows.GetProcessId(receiver)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(elevationReadyTimeout)
	if err := acceptElevationReceiver(pipe, pid, deadline); err != nil {
		return err
	}
	return transferElevationState(pipe, envelope, func() (ElevationState, bool, error) {
		// READY proves the authenticated receiver has pinned and checked our
		// process handle. Remove the temporary query/wait grant before commit.
		if restore != nil {
			if err := restore(); err != nil {
				return ElevationState{}, false, err
			}
		}
		return refresh()
	}, deadline)
}

func encodeElevationEnvelope(envelope elevationEnvelope) ([]byte, error) {
	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode elevation state: %w", err)
	}
	if len(data) > elevationStateLimit {
		return nil, errors.New("elevation state exceeds size limit")
	}
	return data, nil
}

func transferElevationState(pipe windows.Handle, initial elevationEnvelope, refresh func() (ElevationState, bool, error), deadline time.Time) error {
	data, err := encodeElevationEnvelope(initial)
	if err != nil {
		return err
	}
	if err := sendElevationFrame(pipe, elevationInitial, data, deadline); err != nil {
		return err
	}
	if _, err := readElevationFrame(pipe, elevationReady, deadline); err != nil {
		return err
	}
	state, ok, err := refresh()
	if err != nil {
		return fmt.Errorf("refresh elevation state: %w", err)
	}
	if !ok {
		return fmt.Errorf("refresh elevation state: %w", windows.ERROR_CANCELLED)
	}
	if state.SourceConfigDigest != initial.State.SourceConfigDigest || len(state.Entries) != len(initial.State.Entries) {
		return errors.New("configuration changed before final elevation snapshot")
	}
	initial.State = state
	data, err = encodeElevationEnvelope(initial)
	if err != nil {
		return err
	}
	if err := sendElevationFrame(pipe, elevationFinal, data, deadline); err != nil {
		return err
	}
	if _, err := readElevationFrame(pipe, elevationFinalReady, deadline); err != nil {
		return err
	}
	return sendElevationFrame(pipe, elevationCommit, nil, deadline)
}

func decodeElevationEnvelope(data []byte, target, argument string, sourcePID uint32) (*elevationEnvelope, error) {
	target, configPath, err := elevationIdentity(target, argument)
	if err != nil {
		return nil, err
	}
	if len(data) > elevationStateLimit {
		return nil, errors.New("elevation state exceeds size limit")
	}
	var envelope elevationEnvelope
	// A missing role must not silently become a request for normal privilege.
	wire := struct {
		*elevationEnvelope
		TargetElevated *bool
	}{elevationEnvelope: &envelope}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("trailing elevation state data")
	}
	if wire.TargetElevated == nil || *wire.TargetElevated != IsElevated() {
		return nil, errors.New("elevation receiver privilege mismatch")
	}
	envelope.TargetElevated = *wire.TargetElevated
	if sourcePID == 0 || envelope.SourcePID != sourcePID || !strings.EqualFold(envelope.Target, target) || !strings.EqualFold(envelope.ConfigPath, configPath) {
		return nil, errors.New("elevation state identity mismatch")
	}
	cfg, stamp, err := config.LoadSnapshot(configPath)
	if err != nil {
		return nil, fmt.Errorf("load elevation configuration: %w", err)
	}
	if !envelope.TargetElevated && cfg.RequireAdmin {
		return nil, errors.New("configuration requires administrator privilege")
	}
	if envelope.State.SourceConfigDigest == ([32]byte{}) || envelope.State.SourceConfigDigest != cfg.SourceDigest {
		return nil, errors.New("configuration changed before elevation handoff")
	}
	if len(cfg.Configs) != len(envelope.State.Entries) {
		return nil, errors.New("elevation entries do not match configuration")
	}
	envelope.State.Config, envelope.State.ConfigStamp = cfg, stamp
	return &envelope, nil
}

func openElevationSource(pipe windows.Handle, name, target string) (windows.Handle, uint32, error) {
	expected, err := elevationPipeSource(name)
	if err != nil {
		return 0, 0, err
	}
	actual, err := pipeProcessID(pipe, true)
	if err != nil {
		return 0, 0, err
	}
	if actual != expected || actual == uint32(os.Getpid()) {
		return 0, 0, errors.New("elevation pipe server identity mismatch")
	}
	source, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, actual)
	if err != nil {
		return 0, 0, fmt.Errorf("open elevation source: %w", err)
	}
	// Hold the process object before acknowledging anything (PID reuse cannot
	// redirect the later exit wait). Require the same physical executable too.
	var image [32768]uint16
	size := uint32(len(image))
	err = windows.QueryFullProcessImageName(source, 0, &image[0], &size)
	if err == nil {
		var executable, requested os.FileInfo
		executable, err = os.Stat(windows.UTF16ToString(image[:size]))
		if err == nil {
			requested, err = os.Stat(target)
		}
		if err == nil && !os.SameFile(executable, requested) {
			err = errors.New("elevation source executable mismatch")
		}
	}
	if err != nil {
		windows.CloseHandle(source)
		return 0, 0, err
	}
	return source, actual, nil
}

func connectElevationPipe(name string, deadline time.Time) (windows.Handle, error) {
	if _, err := elevationPipeSource(name); err != nil {
		return 0, err
	}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	for time.Now().Before(deadline) {
		// Identification-level SQOS prevents a malicious server impersonating admin.
		pipe, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
		if err == nil {
			return pipe, nil
		}
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) && !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return 0, err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return 0, errors.New("timed out connecting elevation pipe")
}

// ReceiveElevation never authorizes restart on EOF, timeout, or partial commit.
func ReceiveElevation(name, target, configArgument string) (*ElevationState, error) {
	deadline := time.Now().Add(elevationReadyTimeout + 10*time.Second)
	pipe, err := connectElevationPipe(name, deadline)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(pipe)
	source, pid, err := openElevationSource(pipe, name, target)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(source)
	state, err := receiveElevationState(pipe, target, configArgument, pid, deadline)
	if err != nil {
		return nil, err
	}
	// A committed source may need arbitrarily long to stop its children safely.
	status, err := windows.WaitForSingleObject(source, windows.INFINITE)
	if err != nil {
		return nil, fmt.Errorf("wait for elevation source exit: %w", err)
	}
	if status != windows.WAIT_OBJECT_0 {
		return nil, errors.New("unexpected elevation source wait result")
	}
	return state, nil
}

func receiveElevationState(pipe windows.Handle, target, argument string, pid uint32, deadline time.Time) (*ElevationState, error) {
	data, err := readElevationFrame(pipe, elevationInitial, deadline)
	if err != nil {
		return nil, err
	}
	initial, err := decodeElevationEnvelope(data, target, argument, pid)
	if err != nil {
		return nil, err
	}
	if err := sendElevationFrame(pipe, elevationReady, nil, deadline); err != nil {
		return nil, err
	}
	data, err = readElevationFrame(pipe, elevationFinal, deadline)
	if err != nil {
		return nil, err
	}
	final, err := decodeElevationEnvelope(data, target, argument, pid)
	if err != nil {
		return nil, err
	}
	if final.TargetElevated != initial.TargetElevated || final.State.SourceConfigDigest != initial.State.SourceConfigDigest || len(final.State.Entries) != len(initial.State.Entries) {
		return nil, errors.New("final elevation state identity mismatch")
	}
	if err := sendElevationFrame(pipe, elevationFinalReady, nil, deadline); err != nil {
		return nil, err
	}
	if _, err := readElevationFrame(pipe, elevationCommit, deadline); err != nil {
		return nil, err
	}
	return &final.State, nil
}
