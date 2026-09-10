package app

import "testing"

func TestSplitExecutable(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		executable string
		parameters string
	}{
		{name: "relative", command: `cmd.exe /c echo ok`, executable: `cmd.exe`, parameters: `/c echo ok`},
		{name: "unquoted absolute path", command: `C:\Program Files\Demo\demo.EXE --serve`, executable: `C:\Program Files\Demo\demo.EXE`, parameters: `--serve`},
		{name: "quoted path", command: `"C:\Program Files\Demo\demo.exe" "a b"`, executable: `C:\Program Files\Demo\demo.exe`, parameters: `"a b"`},
		{name: "exe directory", command: `C:\dir.exe\tool.exe --serve`, executable: `C:\dir.exe\tool.exe`, parameters: `--serve`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			executable, parameters, err := SplitExecutable(tc.command)
			if err != nil {
				t.Fatal(err)
			}
			if executable != tc.executable || parameters != tc.parameters {
				t.Fatalf("SplitExecutable() = (%q, %q), want (%q, %q)", executable, parameters, tc.executable, tc.parameters)
			}
		})
	}
}

func TestSplitExecutableRejectsInvalidCommand(t *testing.T) {
	for _, command := range []string{"", `"unterminated.exe`, "script.cmd", `"script.cmd"`, `"demo.exe.bak"`, `demo.exe.bak`} {
		if _, _, err := SplitExecutable(command); err == nil {
			t.Fatalf("SplitExecutable(%q) succeeded, want error", command)
		}
	}
}
