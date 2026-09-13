//go:build windows && amd64

package win32

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fcying/CommandTrayHostGo/internal/config"
	"golang.org/x/sys/windows"
)

func TestElevationPipeSourceProcess(t *testing.T) {
	parent := os.Getenv("CTH_ELEVATION_PIPE_PARENT")
	if parent == "" {
		return
	}
	pid, err := strconv.ParseUint(parent, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	pipe, name, err := createElevationPipe("")
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(pipe)
	fmt.Println(name)
	if err := acceptElevationReceiver(pipe, uint32(pid), time.Now().Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	// Parent explicitly releases us after checking the pinned source handle.
	var release [1]byte
	_, _ = os.Stdin.Read(release[:])
}

func TestElevationSourceRequiresPhysicalExecutableAndPinsExit(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestElevationPipeSourceProcess$")
	command.Env = append(os.Environ(), "CTH_ELEVATION_PIPE_PARENT="+strconv.Itoa(os.Getpid()))
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	name, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	name = strings.TrimSpace(name)
	client, err := connectElevationPipe(name, time.Now().Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(client)
	wrong := filepath.Join(t.TempDir(), "wrong.exe")
	if err := os.WriteFile(wrong, []byte("not the source executable"), 0600); err != nil {
		t.Fatal(err)
	}
	if source, _, err := openElevationSource(client, name, wrong); err == nil {
		windows.CloseHandle(source)
		t.Fatal("different executable accepted")
	}
	source, pid, err := openElevationSource(client, name, os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(source)
	if pid != uint32(command.Process.Pid) {
		t.Fatal("source PID mismatch")
	}
	if status, err := windows.WaitForSingleObject(source, 0); err != nil || status != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("source handle not pinned to live child: %d %v", status, err)
	}
	if _, err := stdin.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if status, err := windows.WaitForSingleObject(source, 5000); err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("source exit not observed: %d %v", status, err)
	}
}

func elevationTestPair(t *testing.T) (windows.Handle, windows.Handle) {
	t.Helper()
	server, name, err := createElevationPipe("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.CloseHandle(server) })
	deadline := time.Now().Add(3 * time.Second)
	accepted := make(chan error, 1)
	go func() { accepted <- acceptElevationReceiver(server, uint32(os.Getpid()), deadline) }()
	client, err := connectElevationPipe(name, deadline)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.CloseHandle(client) })
	if err := <-accepted; err != nil {
		t.Fatal(err)
	}
	return server, client
}

func elevationTestEnvelope(t *testing.T) elevationEnvelope {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := []byte(`{"configs":[{"name":"one","path":".","cmd":"cmd.exe","working_directory":"","addition_env_path":"","use_builtin_console":false,"is_gui":false,"enabled":true}]}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	return elevationEnvelope{SourcePID: uint32(os.Getpid()), Target: filepath.Join(dir, "host.exe"), ConfigPath: path, TargetElevated: IsElevated(), State: ElevationState{SourceConfigDigest: cfg.SourceDigest, Entries: []ElevationEntry{{Enabled: true, Show: true, Launched: true}}}}
}

func TestElevationRejectsUnauthorizedClientWithoutState(t *testing.T) {
	server, name, err := createElevationPipe("")
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(server)
	deadline := time.Now().Add(200 * time.Millisecond)
	done := make(chan error, 1)
	// The connected process is this test, not the process authorized by source.
	go func() { done <- acceptElevationReceiver(server, uint32(os.Getpid())+1, deadline) }()
	client, err := connectElevationPipe(name, deadline)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(client)
	var first [1]byte
	if err := elevationBytes(client, first[:], false, deadline); err == nil {
		t.Fatal("unauthorized client received state")
	}
	if err := <-done; err == nil {
		t.Fatal("unauthorized client authenticated")
	}
}

func TestElevationRejectsWrongKernelServerIdentity(t *testing.T) {
	server, client := elevationTestPair(t)
	_ = server
	forged := elevationPipePrefix + fmt.Sprint(uint32(os.Getpid())+1) + "." + strings.Repeat("a", 64)
	source, _, err := openElevationSource(client, forged, os.Args[0])
	if err == nil {
		windows.CloseHandle(source)
		t.Fatal("pipe name substituted for kernel server identity")
	}
}

func TestElevationFinalSnapshotTransfer(t *testing.T) {
	server, client := elevationTestPair(t)
	initial := elevationTestEnvelope(t)
	deadline := time.Now().Add(3 * time.Second)
	done := make(chan error, 1)
	go func() {
		done <- transferElevationState(server, initial, func() (ElevationState, bool, error) {
			final := initial.State
			final.Entries = []ElevationEntry{{Enabled: true, CronTransient: true}}
			return final, true, nil
		}, deadline)
	}()
	final, err := receiveElevationState(client, initial.Target, initial.ConfigPath, initial.SourcePID, deadline)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	want := ElevationEntry{Enabled: true, CronTransient: true}
	if final.Entries[0] != want {
		t.Fatalf("receiver used stale snapshot: %+v", final.Entries[0])
	}
	if final.Config.SourceDigest != initial.State.SourceConfigDigest {
		t.Fatal("validated configuration lost")
	}
}

func TestElevationRefreshPreservesErrorAndCancellation(t *testing.T) {
	failure := errors.New("query failed")
	for _, queryErr := range []error{failure, nil} {
		t.Run(fmt.Sprint(queryErr), func(t *testing.T) {
			server, client := elevationTestPair(t)
			initial := elevationTestEnvelope(t)
			deadline := time.Now().Add(time.Second)
			done := make(chan error, 1)
			go func() {
				done <- transferElevationState(server, initial, func() (ElevationState, bool, error) { return ElevationState{}, false, queryErr }, deadline)
			}()
			if _, err := readElevationFrame(client, elevationInitial, deadline); err != nil {
				t.Fatal(err)
			}
			if err := sendElevationFrame(client, elevationReady, nil, deadline); err != nil {
				t.Fatal(err)
			}
			want := queryErr
			if want == nil {
				want = windows.ERROR_CANCELLED
			}
			if err := <-done; !errors.Is(err, want) {
				t.Fatalf("refresh result lost: %v", err)
			}
			if _, err := readElevationFrame(client, elevationFinal, time.Now().Add(20*time.Millisecond)); err == nil {
				t.Fatal("failed refresh published state")
			}
		})
	}
}

func TestElevationNoInitialAcknowledgementDoesNotRefresh(t *testing.T) {
	server, client := elevationTestPair(t)
	initial := elevationTestEnvelope(t)
	done := make(chan error, 1)
	called := false
	deadline := time.Now().Add(100 * time.Millisecond)
	go func() {
		done <- transferElevationState(server, initial, func() (ElevationState, bool, error) { called = true; return initial.State, true, nil }, deadline)
	}()
	if _, err := readElevationFrame(client, elevationInitial, deadline); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("missing readiness was accepted")
	}
	if called {
		t.Fatal("refresh ran before readiness")
	}
}

func TestElevationDisconnectOrTimeoutBeforeCommitCannotActivate(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(fmt.Sprint(disconnect), func(t *testing.T) {
			server, client := elevationTestPair(t)
			initial := elevationTestEnvelope(t)
			deadline := time.Now().Add(300 * time.Millisecond)
			done := make(chan error, 1)
			go func() {
				_, err := receiveElevationState(client, initial.Target, initial.ConfigPath, initial.SourcePID, deadline)
				done <- err
			}()
			data, err := encodeElevationEnvelope(initial)
			if err != nil {
				t.Fatal(err)
			}
			for _, phase := range []struct{ state, ack byte }{{elevationInitial, elevationReady}, {elevationFinal, elevationFinalReady}} {
				if err := sendElevationFrame(server, phase.state, data, deadline); err != nil {
					t.Fatal(err)
				}
				if _, err := readElevationFrame(server, phase.ack, deadline); err != nil {
					t.Fatal(err)
				}
			}
			if disconnect {
				if err := windows.DisconnectNamedPipe(server); err != nil {
					t.Fatal(err)
				}
			}
			if err := <-done; err == nil {
				t.Fatal("receiver activated without commit")
			}
		})
	}
}

func TestElevationMissingFinalAcknowledgementCannotCommit(t *testing.T) {
	server, client := elevationTestPair(t)
	initial := elevationTestEnvelope(t)
	deadline := time.Now().Add(200 * time.Millisecond)
	done := make(chan error, 1)
	go func() {
		done <- transferElevationState(server, initial, func() (ElevationState, bool, error) { return initial.State, true, nil }, deadline)
	}()
	if _, err := readElevationFrame(client, elevationInitial, deadline); err != nil {
		t.Fatal(err)
	}
	if err := sendElevationFrame(client, elevationReady, nil, deadline); err != nil {
		t.Fatal(err)
	}
	if _, err := readElevationFrame(client, elevationFinal, deadline); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("handoff committed without final acknowledgement")
	}
	if _, err := readElevationFrame(client, elevationCommit, time.Now().Add(20*time.Millisecond)); err == nil {
		t.Fatal("failed handoff sent commit")
	}
}

func TestElevationConfigMutationRejectsFinalSnapshot(t *testing.T) {
	server, client := elevationTestPair(t)
	initial := elevationTestEnvelope(t)
	deadline := time.Now().Add(time.Second)
	done := make(chan error, 1)
	go func() {
		_, err := receiveElevationState(client, initial.Target, initial.ConfigPath, initial.SourcePID, deadline)
		done <- err
	}()
	data, err := encodeElevationEnvelope(initial)
	if err != nil {
		t.Fatal(err)
	}
	if err := sendElevationFrame(server, elevationInitial, data, deadline); err != nil {
		t.Fatal(err)
	}
	if _, err := readElevationFrame(server, elevationReady, deadline); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(initial.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(initial.ConfigPath, bytes.Replace(original, []byte("cmd.exe"), []byte("bad.exe"), 1), 0600); err != nil {
		t.Fatal(err)
	}
	if err := sendElevationFrame(server, elevationFinal, data, deadline); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("receiver accepted mutated configuration")
	}
	if _, err := readElevationFrame(server, elevationFinalReady, time.Now().Add(20*time.Millisecond)); err == nil {
		t.Fatal("mutated configuration acknowledged")
	}
}

func TestElevationFrameRejectsOversizeBeforeAllocation(t *testing.T) {
	server, client := elevationTestPair(t)
	deadline := time.Now().Add(time.Second)
	header := [5]byte{elevationInitial}
	binary.LittleEndian.PutUint32(header[1:], elevationStateLimit+1)
	done := make(chan error, 1)
	go func() { done <- elevationBytes(server, header[:], true, deadline) }()
	if _, err := readElevationFrame(client, elevationInitial, deadline); err == nil {
		t.Fatal("oversize frame accepted")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestElevationEnvelopeRejectsWrongIdentityAndMalformedJSON(t *testing.T) {
	initial := elevationTestEnvelope(t)
	valid, err := encodeElevationEnvelope(initial)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{[]byte(strings.Repeat(" ", elevationStateLimit+1)), []byte(`{"SourcePID":0}`), []byte(`{} {}`), []byte(`{"unknown":true}`), bytes.Replace(valid, []byte(fmt.Sprintf(`"SourcePID":%d`, initial.SourcePID)), []byte(`"SourcePID":1`), 1)} {
		if _, err := decodeElevationEnvelope(data, initial.Target, initial.ConfigPath, initial.SourcePID); err == nil {
			t.Fatal("invalid envelope accepted")
		}
	}
}

func TestElevationCompactStateSupportsLargeNamesAndDetectsSameStampMutation(t *testing.T) {
	initial := elevationTestEnvelope(t)
	var data strings.Builder
	data.WriteString(`{"configs":[`)
	for i := range config.MaxConfigEntries {
		if i != 0 {
			data.WriteByte(',')
		}
		fmt.Fprintf(&data, `{"name":%q,"path":".","cmd":"cmd.exe","working_directory":"","addition_env_path":"","use_builtin_console":false,"is_gui":false,"enabled":false}`, fmt.Sprintf("%d-%s", i, strings.Repeat("n", 1300)))
	}
	data.WriteString(`]}`)
	if err := os.WriteFile(initial.ConfigPath, []byte(data.String()), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, stamp, err := config.LoadSnapshot(initial.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	initial.State = ElevationState{SourceConfigDigest: cfg.SourceDigest, Entries: make([]ElevationEntry, len(cfg.Configs))}
	initial.State.Entries[len(initial.State.Entries)-1].Enabled = true
	encoded, err := encodeElevationEnvelope(initial)
	if err != nil {
		t.Fatal(err)
	}
	received, err := decodeElevationEnvelope(encoded, initial.Target, initial.ConfigPath, initial.SourcePID)
	if err != nil {
		t.Fatal(err)
	}
	if !received.State.Entries[len(initial.State.Entries)-1].Enabled {
		t.Fatal("last compact entry lost")
	}
	changed := bytes.Replace([]byte(data.String()), []byte("cmd.exe"), []byte("bad.exe"), 1)
	if err := os.WriteFile(initial.ConfigPath, changed, 0600); err != nil {
		t.Fatal(err)
	}
	modified := time.Unix(0, stamp.ModTime)
	if err := os.Chtimes(initial.ConfigPath, modified, modified); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeElevationEnvelope(encoded, initial.Target, initial.ConfigPath, initial.SourcePID); err == nil {
		t.Fatal("same-size same-timestamp configuration mutation accepted")
	}
}

func TestElevationRejectsWrongPrivilegeBeforeAcknowledgement(t *testing.T) {
	for _, phase := range []byte{elevationInitial, elevationFinal} {
		t.Run(fmt.Sprint(phase), func(t *testing.T) {
			server, client := elevationTestPair(t)
			initial := elevationTestEnvelope(t)
			deadline := time.Now().Add(time.Second)
			done := make(chan error, 1)
			go func() {
				_, err := receiveElevationState(client, initial.Target, initial.ConfigPath, initial.SourcePID, deadline)
				done <- err
			}()
			if phase == elevationFinal {
				data, err := encodeElevationEnvelope(initial)
				if err != nil {
					t.Fatal(err)
				}
				if err := sendElevationFrame(server, elevationInitial, data, deadline); err != nil {
					t.Fatal(err)
				}
				if _, err := readElevationFrame(server, elevationReady, deadline); err != nil {
					t.Fatal(err)
				}
			}
			initial.TargetElevated = !initial.TargetElevated
			data, err := encodeElevationEnvelope(initial)
			if err != nil {
				t.Fatal(err)
			}
			if err := sendElevationFrame(server, phase, data, deadline); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err == nil {
				t.Fatal("receiver accepted wrong privilege")
			}
			ack := elevationReady
			if phase == elevationFinal {
				ack = elevationFinalReady
			}
			if _, err := readElevationFrame(server, ack, time.Now().Add(20*time.Millisecond)); err == nil {
				t.Fatal("wrong privilege acknowledged")
			}
		})
	}
}

func TestElevationRejectsMissingPrivilege(t *testing.T) {
	initial := elevationTestEnvelope(t)
	data, err := encodeElevationEnvelope(initial)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(fmt.Sprintf(`,"TargetElevated":%t`, initial.TargetElevated)), nil, 1)
	if _, err := decodeElevationEnvelope(data, initial.Target, initial.ConfigPath, initial.SourcePID); err == nil {
		t.Fatal("missing receiver privilege accepted")
	}
}

func TestNormalElevationReceiverRejectsAdministratorConfiguration(t *testing.T) {
	if IsElevated() {
		t.Skip("requires a normal receiver token")
	}
	initial := elevationTestEnvelope(t)
	original, err := os.ReadFile(initial.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := bytes.Replace(original, []byte(`{"configs":`), []byte(`{"require_admin":true,"configs":`), 1)
	if err := os.WriteFile(initial.ConfigPath, updated, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadSnapshot(initial.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	initial.State.SourceConfigDigest = cfg.SourceDigest
	data, err := encodeElevationEnvelope(initial)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeElevationEnvelope(data, initial.Target, initial.ConfigPath, initial.SourcePID); err == nil {
		t.Fatal("normal receiver accepted administrator-only configuration")
	}
}
