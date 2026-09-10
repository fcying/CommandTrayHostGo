package app

import (
	"strings"
	"testing"

	"github.com/fcying/CommandTrayHostGo/internal/i18n"
)

func TestAboutText(t *testing.T) {
	previousVersion, previousBuildTime := Version, BuildTime
	Version, BuildTime = "v1.2.3", "2026-07-24T12:34:56Z"
	t.Cleanup(func() {
		Version, BuildTime = previousVersion, previousBuildTime
	})
	text := AboutText("CommandTrayHost", i18n.English)
	for _, want := range []string{
		"CommandTrayHost",
		"Version: v1.2.3",
		"Revision:",
		"Author: fcying",
		"Build Timestamp: 2026-07-24T12:34:56Z",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("AboutText() = %q, missing %q", text, want)
		}
	}
	chinese := AboutText("CommandTrayHost", i18n.SimplifiedChinese)
	for _, want := range []string{"版本: v1.2.3", "修订:", "作者: fcying", "编译时间: 2026-07-24T12:34:56Z"} {
		if !strings.Contains(chinese, want) {
			t.Fatalf("AboutText() = %q, missing %q", chinese, want)
		}
	}
	Version, BuildTime = "development", "unknown"
	development := AboutText("CommandTrayHost", i18n.SimplifiedChinese)
	if !strings.Contains(development, "版本: 开发版本") || !strings.Contains(development, "编译时间: 未知") {
		t.Fatalf("AboutText() = %q", development)
	}
}
