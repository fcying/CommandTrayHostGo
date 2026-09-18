set default-script
set export
set script-interpreter := ["bash", "-euo", "pipefail"]

name := "CommandTrayHostGo"
out := "dist"
GOPROXY := env("CTH_GOPROXY", "https://proxy.golang.org,direct")
release_repository := env("CTH_RELEASE_REPOSITORY", "fcying/CommandTrayHostGo")

_default: build

test:
    go test ./internal/... ./cmd/releasetool

fmt:
    gofmt -w cmd internal

check: test
    GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./cmd/commandtrayhost ./cmd/releasetool ./internal/...

build:
    mkdir -p "{{out}}/windows-amd64"
    version="$(go run ./cmd/releasetool source-version --source internal/app/about.go)"
    resource="cmd/commandtrayhost/commandtrayhost_resources_windows_amd64.syso"
    trap 'rm -f "$resource"' EXIT
    go run ./cmd/releasetool windows-resource --version "$version" --manifest cmd/commandtrayhost/commandtrayhost.manifest --icon cmd/commandtrayhost/taskbar.ico --small-icon cmd/commandtrayhost/small.ico --out "$resource"
    GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-H=windowsgui -s -w -X github.com/fcying/CommandTrayHostGo/internal/app.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ) -X github.com/fcying/CommandTrayHostGo/internal/updater.Repository={{release_repository}}" -o "{{out}}/windows-amd64/{{name}}.exe" ./cmd/commandtrayhost

clean:
    rm -rf "{{out}}" cmd/commandtrayhost/commandtrayhost_resources_windows_amd64.syso
