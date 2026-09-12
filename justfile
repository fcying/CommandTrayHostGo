set shell := ["bash", "-euc"]

name := "CommandTrayHostGo"
out_dir := "dist"
go_proxy := env_var_or_default("CTH_GOPROXY", "https://proxy.golang.org,direct")
release_repository := env_var_or_default("CTH_RELEASE_REPOSITORY", "fcying/CommandTrayHostGo")

_default: build

test:
    GOPROXY="{{go_proxy}}" go test ./internal/... ./cmd/releasetool

fmt:
    gofmt -w cmd internal

check: test
    GOPROXY="{{go_proxy}}" GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./cmd/commandtrayhost ./cmd/releasetool ./internal/...

build:
    mkdir -p "{{out_dir}}/windows-amd64"
    version="$(GOPROXY="{{go_proxy}}" go run ./cmd/releasetool source-version --source internal/app/about.go)"; \
        resource="cmd/commandtrayhost/commandtrayhost_resources_windows_amd64.syso"; \
        trap 'rm -f "$resource"' EXIT; \
        GOPROXY="{{go_proxy}}" go run ./cmd/releasetool windows-resource --version "$version" --manifest cmd/commandtrayhost/commandtrayhost.manifest --icon cmd/commandtrayhost/taskbar.ico --small-icon cmd/commandtrayhost/small.ico --out "$resource"; \
        GOPROXY="{{go_proxy}}" GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-H=windowsgui -s -w -X github.com/fcying/CommandTrayHostGo/internal/app.Version=$version -X github.com/fcying/CommandTrayHostGo/internal/app.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ) -X github.com/fcying/CommandTrayHostGo/internal/updater.Repository={{release_repository}}" -o "{{out_dir}}/windows-amd64/{{name}}.exe" ./cmd/commandtrayhost; \
        GOPROXY="{{go_proxy}}" go run ./cmd/releasetool pe-resources --exe "{{out_dir}}/windows-amd64/{{name}}.exe" --version "$version"
    mkdir -p ~/workspace/CommandTrayHostGo
    cp "{{out_dir}}/windows-amd64/{{name}}.exe" ~/workspace/CommandTrayHostGo

clean:
    rm -rf "{{out_dir}}" cmd/commandtrayhost/commandtrayhost_resources_windows_amd64.syso
