# CommandTrayHostGo

English | [简体中文](README.zh-CN.md)

CommandTrayHostGo is a Windows tray application for managing long-running programs. It starts programs from `config.json` and provides centralized process, window, hotkey, scheduling, and runtime-state controls through a tray menu.

This project started as a Go rewrite of the C++ [rexdf/CommandTrayHost](https://github.com/rexdf/CommandTrayHost) project. Its process controller, configuration handling, tray UI, and release tooling are now implemented and maintained independently in Go. It targets the same tray-based process-management feature set and is not an in-place upgrade from the original application.

## Features

- Start, stop, restart, show, and hide programs from the tray menu.
- Manage child processes with Windows Job Objects and clean them up when the host exits.
- Control window position, size, opacity, topmost state, and icon.
- Support GUI applications and traditional Windows console applications.
- Support global and per-entry hotkeys, nested menu groups, and mutually exclusive entries.
- Schedule actions with six-field, local-time cron expressions.
- Cache enabled, show, position, size, and opacity state.
- Watch the configuration file and apply transactional hot reloads.
- Maintain an independent singleton and Start on Boot registration for each physical EXE path.
- Check GitHub Releases without downloading or executing release assets.
- Provide English and Simplified Chinese interfaces.

## Requirements

- Windows 10 or Windows Server 2016 and later.
- amd64/x64.

Windows x86/386, Windows XP/7/8/8.1, and non-Windows platforms are not supported.

## Install and run

1. Download `CommandTrayHost-<version>-windows-amd64.zip` from [Releases](https://github.com/fcying/CommandTrayHostGo/releases).
2. Extract it to a dedicated directory writable by the current user.
3. Run `CommandTrayHost.exe`.
4. Right-click the tray icon to open the menu.

On first launch, the application creates `config.json` beside the EXE. The generated file contains two disabled test entries:

- `charmap.exe` for GUI window control.
- `cmd.exe /d /k ping -t 1.1.1.1` for console process control.

The application does not enable these entries automatically. Enable them manually from the tray menu when needed.
The template language is selected by automatic locale detection only when the file is missing. Both templates contain `lang: "auto"`. An existing configuration is loaded without rewriting its comments, entry names, or groups when the locale or UI language changes.


Runtime state intentionally lives beside the EXE or selected configuration file. Do not install the application in a directory that the current user cannot write to.

## Command line

Use the default configuration:

```powershell
.\CommandTrayHost.exe
```

Select another configuration:

```powershell
.\CommandTrayHost.exe -c configs\work.json
.\CommandTrayHost.exe -c "D:\CTH Config\work.json"
```

Rules:

- A relative `-c` path is resolved from the final EXE directory, not the caller's current directory.
- The default configuration is `<EXE directory>\config.json`.
- The default cache is `<EXE directory>\command_tray_host.cache`.
- With `-c`, the cache is `<config path>.cache`.
- Missing configuration directories are created automatically.
- Start on Boot preserves the active `-c` argument.
- Only one instance may run for a final physical EXE path. Copies in different directories may run together.
- Junctions and symlinks resolve to the final physical path and cannot bypass the singleton or create separate runtime state.

## Configuration format

`config.json` supports:

- `//` and `/* ... */` comments.
- Trailing commas.
- UTF-8, UTF-16, and UTF-32 input.
- Case-insensitive known field names.
- Up to 3584 entries in `configs`.
- A maximum configuration file size of 100 MiB.

Known optional fields must be omitted rather than set to `null`. Unknown fields, including the legacy root `global`, are ignored and have no runtime effect; a misspelled field does not enable a feature. The reserved cron field `need_renew` is explicitly rejected.

Windows paths may use `/` to avoid escaping `\` in JSON strings.

### Minimal example

```jsonc
{
  // Automatic update checks are enabled by default; this example disables them.
  "lang": "auto",
  "auto_update": false,
  "enable_cache": true,
  "left_click": [0],
  "configs": [
    {
      "name": "Console",
      "path": "C:/Windows/System32",
      "cmd": "cmd.exe /d /k echo CommandTrayHost",
      "working_directory": "",
      "addition_env_path": "",
      "use_builtin_console": false,
      "is_gui": false,
      "enabled": false,
      "start_show": true,
      "stop_cmd": "",
      "kill_timeout": 500,
      "kill_process_tree": true
    }
  ]
}
```

Every entry must explicitly contain these fields, even when their values are empty or `false`:

```text
name
path
cmd
working_directory
addition_env_path
use_builtin_console
is_gui
enabled
```

`addition_env_path` and `use_builtin_console` are currently reserved and have no runtime effect.

## Root configuration fields

### General behavior

| Field | Default | Description |
|---|---:|---|
| `lang` | `auto` | An empty string or exact `auto` uses locale detection below. Exact `zh-CN`, `zh-Hans`, `zh`, and `zh-SG` select Simplified Chinese; other values select English. Values are case-sensitive and are not trimmed. |
| `require_admin` | `false` | Relaunch the entire CommandTrayHost process through UAC during startup. |
| `start_show_silent` | `true` | Start managed programs hidden before applying the target window state to reduce visible flashing. |
| `left_click` | `[]` | Toggle the listed zero-based `configs` indexes on a tray left-click. Every index must refer to an existing entry. An empty array toggles the built-in control console. |
| `enable_groups` | `false` | Build the entry menu hierarchy only when `true` and `groups` is present. |
| `groups` | unset | Menu items containing `configs` indexes or nested group objects. |
| `groups_menu_symbol` | `+` | Prefix displayed before group submenu names. |
| `cmd_menu_max_length` | `0` | Range `0..2147483647`. Keep at most this many Unicode characters from path/cmd text, then append `...` if truncated. `0` disables truncation. |
| `icon` | built-in icon | Tray `.ico` file, also applied to the built-in control console. Relative paths use the EXE directory. Elevation does not replace the product icon with a shield icon. |
| `icon_size` | `256` | Tray icon load size. Must be `16`, `32`, or `256`. |
| `configs` | required | Array containing at least one entry. |

Automatic detection reads the user default locale name, system default LCID, and system ANSI code page, not the Windows display-language setting. Any Simplified Chinese locale name listed above, LCID `2052`, or ACP `936` selects Chinese; otherwise it selects English. Explicit `lang` overrides all three signals.

Hot reload changes built-in menus, prompts, and About only after committing the candidate configuration. The reload prompt uses the current language; cancellation or a failed candidate keeps that language. User-supplied entry/group names are not translated. Native Windows button labels and technical error details do not follow the application catalog.

### Cache and hot reload

| Field | Default | Description |
|---|---:|---|
| `enable_cache` | see below | Enable the runtime state cache. |
| `conform_cache_expire` | `true` | Control the configuration file watcher. `false` disables hot reload but does not disable the startup cache-expiration check. |
| `disable_cache_position` | `false` | Do not save or restore window position. |
| `disable_cache_size` | `false` | Do not save or restore window size. |
| `disable_cache_enabled` | `true` | Do not save or restore the entry enabled state. |
| `disable_cache_show` | `false` | Do not save or restore the window show state. |
| `disable_cache_alpha` | `false` | Do not save or restore opacity. |
| `auto_hot_reloading_config` | `false` | Apply configuration changes without prompting while preserving compatible running processes and cached state. |

The cache is disabled when all cache-related fields are absent. Once any cache field is configured, an omitted `enable_cache` behaves as `true`. Setting `enable_cache` explicitly is recommended.

At startup, `auto_hot_reloading_config=true` also keeps and rebases an expired cache without asking; it is not limited to watcher-triggered reloads.

When the configuration timestamp or size changes:

- With `auto_hot_reloading_config=false`, the application asks whether to discard state and restart managed programs, preserve compatible state, or cancel that reload.
- With `auto_hot_reloading_config=true`, compatible state is preserved automatically.
- Cron, icon, hotkey, and cache changes are staged before commit. Any failure keeps the active configuration unchanged.
- A reload that newly requires administrator rights is rejected when the host is not elevated.
- Disabled entries are still fully validated; `enabled=false` does not bypass invalid cron, hotkey, or group configuration.
- Changing `repeat_mod_hotkey` requires restarting CommandTrayHost.

### Hotkeys

| Field | Default | Description |
|---|---:|---|
| `enable_hotkey` | `true` | Register global and per-entry hotkeys. Disabling registration does not skip syntax or duplicate-binding validation. |
| `repeat_mod_hotkey` | `false` | Allow keyboard auto-repeat to generate repeated hotkey events. |
| `show_hotkey_in_menu` | `true` | Display registered hotkeys in menu labels. |
| `global_hotkey_alpha_step` | `5` | Opacity change per global hotkey event, in the range `1..255`. |
| `hotkey` | empty object | Global hotkey mapping. |

Global `hotkey` actions:

| Field | Action |
|---|---|
| `disable_all` | Stop managed entries except entries with `ignore_all`. |
| `enable_all` | Start entries except entries with `ignore_all`. |
| `hide_all` / `show_all` | Hide or show all running entry windows. |
| `restart_all` | Restart managed entries except entries with `ignore_all`. |
| `elevate` | Relaunch the host as administrator; rejected while managed entries are running. |
| `exit` | Exit the host. |
| `left_click` / `right_click` | Invoke the corresponding tray action. |
| `add_alpha` / `minus_alpha` | Adjust the foreground window opacity. |
| `topmost` | Toggle the foreground window's topmost state. |
| `hide_current` | Hide the foreground window and add it to Docked Windows. |
| `show_all_docked` | Restore all Docked Windows. |

Hotkey syntax is case-insensitive. It supports `Alt`, `Ctrl`, `Shift`, `Win`, digits, letters, and `0xNN` virtual-key codes. The following root-level fragment must be merged into a configuration containing `configs`:

```jsonc
{
  "hotkey": {
    "hide_all": "Ctrl+Alt+H",
    "show_all": "Ctrl+Alt+S",
    "topmost": "Win+Alt+T"
  }
}
```

Special key forms:

- `Ctrl++`: plus.
- `Ctrl+-`: minus.
- `Alt+ `: space.
- `Ctrl+Alt+0x26`: virtual-key code `0x26`.

A hotkey cannot be assigned to more than one action.

### Update checks

| Field | Default | Description |
|---|---:|---|
| `auto_update` | `true` | Check GitHub Releases at startup for formal, comparable builds. The generated test configuration explicitly sets this to `false`. |
| `skip_prerelease` | `true` | Ignore prereleases. |

Only SemVer tags such as `v1.2.3` and `v1.2.3-rc.1` are accepted. Each checker is bound to one validated `owner/repository` identity and returns one explicit outcome: unknown current version, up to date, or update available.

- Requests use the GitHub Releases API over HTTPS, accept at most 1 MiB of JSON, and reject redirects outside `https://api.github.com`.
- Draft releases and unsupported tags are ignored. `skip_prerelease=true` also excludes prereleases before selecting the highest SemVer.
- Transport errors, HTTP 408/5xx, and confirmed 403/429 rate limits are retried at most five times. Server retry headers take precedence; every wait is context-cancellable.
- Development builds do not check automatically. A manual check may report the latest release without claiming that the current version is comparable.
- The checker ignores response-provided asset and page URLs. It only offers a canonical HTTPS release page synthesized from the validated repository and tag.
- It never downloads, extracts, replaces, or executes release assets.

## Entry configuration fields

| Field | Default | Description |
|---|---:|---|
| `name` | required | Tray menu label, initial console title, and cache identity. Must not be empty or contain NUL. Child programs may subsequently change their own title. |
| `path` | required | Executable base directory. Absolute paths are used directly; relative paths use the EXE directory; may be empty. |
| `cmd` | required | Command and arguments. The executable portion must end in `.exe`. Quote executable paths containing spaces. |
| `working_directory` | required | Empty uses `path`; an absolute path is used directly; a normal relative path uses `path`; a leading `>` makes it relative to the EXE directory. |
| `addition_env_path` | required | Reserved; currently has no runtime effect. |
| `use_builtin_console` | required | Reserved; currently has no runtime effect. |
| `is_gui` | required | `true` finds a GUI window by child PID, preferring a standard unowned top-level window and falling back to a tool window or a window owned by CommandTrayHost; `false` uses an isolated helper to associate a traditional console window. |
| `enabled` | required | Run the entry at startup. Cache settings may override this state. |
| `require_admin` | `false` | Require administrator rights. Managed modes require the host to be elevated first; fully detached mode may launch the entry through UAC. |
| `start_show` | see below | Show the window after startup. Managed entries default to `false`; detached and job-only entries default to `true`. |
| `ignore_all` | `false` | Exclude the entry from Enable All, Disable All, Restart All, and same-`exclusion_id` bulk stops. |
| `position` | unset | `[x, y]`. Values in `0..1` are screen proportions; values greater than `1` are pixels. |
| `size` | unset | `[width, height]`. Values in `0..1` are screen proportions; values greater than `1` are pixels. |
| `alpha` | unset | Window opacity in the range `0..255`. |
| `topmost` | `false` | Keep the target window topmost. |
| `not_host_by_commandtrayhost` | `false` | Fully detached mode: no Job Object assignment, retained process handle, stop control, or window control. |
| `not_monitor_by_commandtrayhost` | `false` | Job-only mode: keep Job Object ownership but no retained process handle, stop control, or window control. Takes precedence when both ownership fields are true. |
| `stop_cmd` | empty | Optional command executed through hidden `cmd.exe /d /s /c` in the entry working directory before the normal stop flow. The normal `WM_CLOSE`/wait/`TerminateProcess` or dedicated Job Object cleanup still runs, even when this command fails. The command must finish on its own. |
| `kill_timeout` | `200` | Range `0..4294967294` milliseconds. Wait for exit when `kill_process_tree=false`; GUI windows first receive a close request. |
| `kill_process_tree` | `false` | `true` immediately terminates the entry's dedicated Job Object, including descendants that remain after `stop_cmd` exits the root process, without waiting for `kill_timeout`. `false` waits for `kill_timeout` before terminating only the root process if needed. |
| `exclusion_id` | unset | Integer in `1..2147483647`. Starting this entry first stops other running managed entries with the same ID, except `ignore_all` peers. Not valid for detached or job-only entries. |
| `hotkey` | empty object | Per-entry hotkey mapping. |
| `crontab_config` | unset | Per-entry cron configuration. |
| `icon` | unset | `.ico` file applied to the target window. Relative paths use the EXE directory. |

`position` and `size` must each contain exactly two finite numbers in `0..2147483647`.

Per-entry `hotkey` actions:

| Field | Action |
|---|---|
| `hide_show` | Toggle the window show state. |
| `disable_enable` | Stop or start the entry. |
| `restart` | Restart a managed entry. |
| `elevate` | Launch the entry as administrator. |

Windows Terminal and other pseudoconsole hosts do not expose a traditional console HWND that can be controlled independently by child PID. Their processes can still be managed, but `is_gui=false` window position, size, opacity, topmost, and show controls are unavailable.

A managed entry may run without a controllable window when it starts hidden and does not request an icon, position, size, opacity, or topmost state. Window discovery begins only when the configuration or a user action requires window control.

## Process ownership

| Configuration | Mode | When the host exits | Runtime control |
|---|---|---|---|
| Both fields are `false` | managed | Killed by the Job Object | Start, stop, restart, and window control |
| `not_monitor_by_commandtrayhost=true` | job-only | Killed by the Job Object | Not monitored after launch; no stop or window control |
| `not_host_by_commandtrayhost=true` | fully detached | Process remains running | Not monitored after launch; no stop or window control |

Normal managed processes are created suspended and resumed only after successful Job Object assignment, preventing escape before assignment. The Job Object uses `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`.

## Groups

Integers in `groups` must be valid zero-based `configs` indexes. A group object requires `name`; omitting its nested `groups` array creates an empty group. The same entry may appear more than once. Maximum nesting depth is 40, with at most 3584 menu items including group objects. The following root-level fragment requires at least three entries in `configs`; it is not a complete configuration:

```jsonc
{
  "enable_groups": true,
  "groups_menu_symbol": "+",
  "groups": [
    0,
    {
      "name": "Tools",
      "groups": [1, 2]
    }
  ]
}
```

## Cron

Cron uses local time and six fields, starting with seconds:

```text
second minute hour day-of-month month day-of-week
```

Field ranges are `0..59` seconds, `0..59` minutes, `0..23` hours, `1..31` days, `1..12` or `JAN..DEC` months, and `0..7` or `SUN..SAT` weekdays; `0` and `7` both mean Sunday. Fields support `*`, comma-separated lists, ascending ranges, and positive `/step` values. Day-of-month and day-of-week also accept a standalone `?`, equivalent to `*`.

Month, day-of-month, and day-of-week must all match (AND, not the DOM/DOW OR rule used by some cron implementations). An expression with no future match within the 400-year search horizon is rejected.

Fragment inside a managed `configs[i]` entry: run every five minutes at second zero on weekdays. Do not place `crontab_config` at the root:

```jsonc
{
  "crontab_config": {
    "enabled": true,
    "crontab": "0 */5 * * * MON-FRI",
    "method": "restart_count_stop",
    "count": 3,
    "start_show": true,
    "log": "cron.log",
    "log_level": 2
  }
}
```

| Field | Default | Description |
|---|---:|---|
| `enabled` | `true` | Enable this schedule. |
| `crontab` | required | Six-field cron expression, up to 255 bytes. |
| `method` | required | `start`, `restart`, `stop`, `start_count_stop`, or `restart_count_stop`. |
| `count` | required | Range `0..2147483647`. `0` repeats indefinitely; a positive value limits the number of executions. |
| `start_show` | cache/`false` | Show after a cron start or restart. An explicit value wins, then cached show state, then `false`. |
| `log` | empty | Log file. Relative paths use the EXE directory. |
| `log_level` | `0` | Range `0..3`. Level 0 logs action results, level 1 adds launch details, and level 2 adds scheduling details; level 3 currently matches level 2. |

Setting this schedule's `enabled=false` does not skip required fields, expression/range validation, or ownership restrictions. `need_renew` is reserved and must not appear in user configuration.

Logs rotate to `.1`, `.2`, and later files after exceeding 10 MiB, with up to 499 rotation destinations.

`restart`, `stop`, and count-stop methods require a managed entry. `start_count_stop` and `restart_count_stop` stop the target after the count is exhausted; a failed final stop is scheduled for retry.

## Tray operations

The right-click menu contains:

- Each entry's path, cmd, Show/Hide, Enable/Disable, Restart, and Run As Administrator actions.
- Hide All and the All submenu.
- Start on Boot.
- Elevate.
- Help, Check for Updates, and About.
- Docked Windows.
- Exit.

Selecting an entry path opens its directory. Selecting its cmd locates the executable in Explorer.

An enabled cron configuration marks the entry's path and cmd rows with check marks. Elevate remains visible after elevation; its check mark indicates that the host is already elevated.

Docked Windows labels use the window caption, then the Win32 class name, then the localized untitled-window label. Selecting one docked window restores and activates it. Show All restores every docked window without choosing one to activate.

Start on Boot writes to `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` for the current Windows user. Registrations are isolated by final EXE path. An instance elevated with different credentials cannot modify the original user's Start on Boot state.

Run value names use a compact, URL-safe encoding of the full executable-path SHA-256 hash (59 characters including the prefix). Windows 10 testing found that 80-character value names were stored successfully but ignored at logon. After upgrading from the old naming format, enable Start on Boot again for each desired instance; old 80-character values are not automatically converted and may be removed after checking their target EXE.

## Build

Requirements:

- Go 1.27.
- [`just`](https://github.com/casey/just).
- WSL2 or another environment capable of cross-compiling a Windows amd64 Go application.

```bash
just test
just check
just build
```

Build output:

```text
dist/windows-amd64/CommandTrayHost.exe
```

The build uses:

```text
GOOS=windows
GOARCH=amd64
CGO_ENABLED=0
```

The release version is maintained only in `internal/app/about.go` as `Version`. `just build` reads that value and generates the amd64 Windows version, manifest, and icon resource before linking. Explorer's Product version matches the in-app version; its mandatory four-part numeric File version is derived as `MAJOR.MINOR.PATCH.0`. The EXE also includes product, filename, company, description, and copyright metadata. The generated resource object is removed after the build.

Optional environment variables:

- `CTH_GOPROXY`: override the Go module proxy.
- `CTH_RELEASE_REPOSITORY`: override the GitHub `owner/repository` embedded at build time; default `fcying/CommandTrayHostGo`.

## License

[MIT](LICENSE). The bundled tray and window icons are reused from [rexdf/CommandTrayHost](https://github.com/rexdf/CommandTrayHost) under its MIT License. The original copyright notice is retained in `LICENSE`.
