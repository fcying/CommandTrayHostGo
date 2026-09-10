# Repository Maintenance Notes

## Project Scope

- This repository contains a Windows-only Go application. Its module path is `github.com/fcying/CommandTrayHostGo`; the product and executable names are `CommandTrayHost` and `CommandTrayHost.exe`.
- Only Windows 10 or later on amd64 is supported. The main application and Win32 implementation use `windows && amd64` build tags.

## Build and Verification

- Develop from WSL2 with `just`: `just test` runs the Go tests, `just check` runs the tests and Windows amd64 vet, `just build` produces the release-form Windows amd64 executable, and `just clean` removes in-repository build output.
- Builds always use `GOOS=windows`, `GOARCH=amd64`, and `CGO_ENABLED=0`. Use `CTH_GOPROXY` to override the dependency proxy and `CTH_RELEASE_REPOSITORY` to override the update repository embedded in the executable.
- `just build` writes `dist/windows-amd64/CommandTrayHost.exe` inside the repository and copies it to `~/workspace/CommandTrayHostGo/CommandTrayHost.exe`.
- The sole version source is `Version` in `internal/app/about.go`. `cmd/releasetool` generates the PE manifest, VERSIONINFO, and icon resources during the build. `cmd/commandtrayhost/commandtrayhost_resources_windows_amd64.syso` is temporary and must not be committed.
- A release ZIP must contain `CommandTrayHost.exe` and the root `LICENSE`; its name is `CommandTrayHost-<version>-windows-amd64.zip`.
- Cross-compilation alone is insufficient for Win32 behavior changes. Run `just check` and `just build` before committing. Changes to the tray, windows, processes, startup registration, hot reload, or session shutdown also require exercising the affected behavior in the Windows test environment.

## Change Conventions

- Use English for Go identifiers, code comments, and Git commit messages. Keep user-facing behavior documentation synchronized between `README.md` and `README.zh-CN.md`.
- Reuse the existing package boundaries and message-loop model. Do not add compatibility shims, speculative configuration fields, or a second state path. For a clean cutover, migrate every caller and remove the obsolete path.
- Keep tests only for observable behavior, boundaries, and ownership invariants. Do not test field forwarding, copied defaults, or source text. Prefer proving Windows API behavior through real Windows operations.
- Do not commit `dist/`, `*.exe`, runtime `config.json`, `*.cache`, logs, test fixtures, evidence archives, or generated `.syso` files.

## Release Behavior

- `.github/workflows/release.yml` runs tests, vet, the Windows build, PE resource verification, and deterministic packaging on pushes to `main` or `dev`.
- A push to `main` creates a formal Release when the source version tag does not exist. Other pushes to `main` or `dev` update the `dev` draft prerelease. Account for this publishing side effect before modifying or pushing either branch.
- The workflow fetches full Git history to inspect tags and generate release notes, and reads the build time from the current commit. Do not duplicate the version in another configuration file or maintain generated resources manually.
- The repository is currently private. Inspecting its Actions runs and Releases requires authenticated GitHub access; an anonymous HTTP 404 does not establish that a run or Release is missing.

## Configuration and Runtime State

- At startup, set both the process working directory and the `CWD` environment variable to the final physical executable directory. The default configuration is `config.json` in that directory; relative `-c` paths are also resolved from it.
- The default cache is `command_tray_host.cache` in the executable directory. With `-c`, the cache is `<config path>.cache`. Keep the configuration and runtime state portable and beside the selected base path.
- `config.json` is JSON-with-comments. It accepts comments, trailing commas, UTF-8/16/32 input, and case-insensitive known field names. A parser replacement must preserve all these inputs and the existing limits.
- Cache validity depends on the configuration and cache timestamps. Hot reload may preserve running children only while cached identity and path data still match. Review configuration, reload, and state-cache changes together.
- Cron expressions always have six fields, including seconds, and use local time. Do not replace this with standard five-field or UTC semantics.

## Runtime Architecture and Invariants

- `cmd/commandtrayhost/main_windows.go` resolves the final executable, configuration, and cache paths; acquires the singleton; loads configuration and cache state; handles elevation; and then starts the Win32 tray application.
- `internal/app` contains platform-independent domain logic. `internal/config` parses, validates, and normalizes configuration. `internal/statecache` owns cache invalidation and reload rebasing. `internal/updater` performs read-only update checks and implements the release protocol.
- `internal/win32/app_windows.go` owns the hidden window, message loop, tray lifecycle, menus, hot reload, and final cache flush. Serialize mutations of UI, configuration, and runtime entry state through the message-loop path.
- `internal/win32/singleton_windows.go` derives its mutex from the final physical executable path. Copies in different directories may run together; a global product-name mutex is a regression. Junctions and symlinks must not bypass the singleton for the same physical executable.
- Managed children use a Job Object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`. Exit, restart, elevation, `not_host_by_commandtrayhost`, `not_monitor_by_commandtrayhost`, and `kill_process_tree` deliberately alter ownership. Do not replace these paths with unconditional process termination or a single cleanup policy.
- User-visible Win32 errors are reported with message boxes. The application has no persistent diagnostic log; only configured cron action logs are written through `internal/cronlog`.

## Windows Test Environment

- Before any infrastructure operation on the test VM, read the API endpoint, account, password, TLS certificate pin, node, and VM ID from `~/.config/commandtrayhost/pve-test.json`. Verify the server certificate against the pin, then use only the Proxmox HTTPS API.
- Never operate the VM through a Proxmox host SSH alias, root SSH, `qm`, or `pvesh`. Guest commands may use the dedicated direct `ssh cth-win10` connection. That host entry must not route through the Proxmox host with `ProxyJump` or `ProxyCommand`.
- Keep every connection and authentication value outside the repository. Never copy one into repository files, command output, commit messages, or agent instructions.
- After Windows verification, restore locale, scaling, Explorer, registry startup entries, and power state. Remove test services, processes, directories, and Run/StartupApproved traces. Store evidence archives only outside the repository.
