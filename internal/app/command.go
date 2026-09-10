package app

import (
	"errors"
	"strings"
)

func SplitExecutable(command string) (string, string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", "", errors.New("command is empty")
	}
	if command[0] == '"' {
		end := strings.Index(command[1:], `"`)
		if end < 0 {
			return "", "", errors.New("command has an unterminated executable quote")
		}
		end++
		executable := command[1:end]
		if !strings.HasSuffix(strings.ToLower(executable), ".exe") {
			return "", "", errors.New("quoted executable must end with .exe")
		}
		return executable, strings.TrimSpace(command[end+1:]), nil
	}
	lower := strings.ToLower(command)
	end := executableEnd(lower)
	if end == 0 {
		return "", "", errors.New("command must contain an .exe executable")
	}
	return strings.TrimSpace(command[:end]), strings.TrimSpace(command[end:]), nil
}

func executableEnd(command string) int {
	const suffix = ".exe"
	for offset := 0; offset < len(command); {
		index := strings.Index(command[offset:], suffix)
		if index < 0 {
			return 0
		}
		end := offset + index + len(suffix)
		if end == len(command) || command[end] == ' ' || command[end] == '\t' || command[end] == '\r' || command[end] == '\n' {
			return end
		}
		offset = end
	}
	return 0
}
