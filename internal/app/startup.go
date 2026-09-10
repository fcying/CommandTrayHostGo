package app

import (
	"crypto/sha256"
	"encoding/base64"
)

func StartupValueName(executablePath string) string {
	sum := sha256.Sum256([]byte(executablePath))
	// Keep the full hash within the name length accepted by Windows startup processing.
	return "CommandTrayHost_" + base64.RawURLEncoding.EncodeToString(sum[:])
}
