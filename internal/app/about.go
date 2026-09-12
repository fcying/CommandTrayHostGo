package app

import (
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/fcying/CommandTrayHostGo/internal/i18n"
)

const projectAuthor = "fcying"

var BuildTime = "unknown"
var Version = "v0.4.0"

func AboutText(product string, language i18n.Language) string {
	text := i18n.Text(language)
	version := Version
	if version == "development" {
		version = text.Development
	}
	revision := text.Unknown
	built := BuildTime
	if built == "unknown" {
		built = text.Unknown
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if Version == "development" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
		dirty := false
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				dirty = setting.Value == "true"
			}
		}
		if dirty && revision != text.Unknown {
			revision += " (" + text.Modified + ")"
		}
	}
	modifiedSuffix := " (" + text.Modified + ")"
	if len(revision) > 12 && !strings.HasSuffix(revision, modifiedSuffix) {
		revision = revision[:12]
	} else if strings.HasSuffix(revision, modifiedSuffix) {
		base := strings.TrimSuffix(revision, modifiedSuffix)
		if len(base) > 12 {
			base = base[:12]
		}
		revision = base + modifiedSuffix
	}
	return fmt.Sprintf("%s\n\n%s: %s\n%s: %s\n%s: %s\n%s: %s", product, text.Version, version, text.Revision, revision, text.Author, projectAuthor, text.BuildTimestamp, built)
}
