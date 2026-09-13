# CommandTrayHostGo

[English](README.md) | 简体中文

CommandTrayHostGo 是一个 Windows 托盘进程管理器. 它从 `config.json` 启动和管理常驻程序, 并通过托盘菜单统一控制进程、窗口、热键、定时任务和运行状态.

本项目始于对 C++ [rexdf/CommandTrayHost](https://github.com/rexdf/CommandTrayHost) 项目的 Go 重写. 进程控制、配置处理、托盘 UI 和发布工具现由本项目使用 Go 独立实现和维护. 目标是提供一致的托盘进程管理功能, 不作为原应用的原地升级版本.

## 功能

- 从托盘菜单启动、停止、重启、显示和隐藏多个程序.
- 使用 Windows Job Object 管理子进程, 宿主退出时清理受管进程.
- 控制窗口位置、尺寸、透明度、置顶状态和图标.
- 支持 GUI 程序和传统 Windows console 程序.
- 支持全局热键、单项热键、菜单分组和互斥启动组.
- 支持包含秒字段的本地时间 cron 调度.
- 缓存 enabled、show、position、size 和 alpha 状态.
- 监控配置文件并事务化热重载.
- 支持每个物理 EXE 路径独立的单例和 Start on Boot.
- 检查 GitHub Release; 内置更新器不会下载或执行发布资产.
- 简体中文和英文界面.

## 系统要求

- Windows 10 或 Windows Server 2016 及更高版本.
- amd64/x64.

不支持 Windows x86/386、Windows XP/7/8/8.1 或非 Windows 平台.

## 安装和启动

1. 从 [Releases](https://github.com/fcying/CommandTrayHostGo/releases) 下载 `CommandTrayHostGo-<version>-windows-amd64.zip`.
2. 解压到一个当前用户可写的独立目录.
3. 运行 `CommandTrayHostGo.exe`.
4. 右键托盘图标打开菜单.

首次启动会在 EXE 目录生成 `config.json`. 默认生成两个禁用的测试项:

- `charmap.exe`, 用于测试 GUI 窗口控制.
- `cmd.exe /d /k ping -t 1.1.1.1`, 用于测试 console 进程控制.

程序不会自动启用这两个测试项. 可从托盘菜单手动启用.
只有配置文件缺失时才按自动检测结果选择中英文模板. 两份模板均包含 `lang: "auto"`. 已有配置直接加载, 不会因区域或界面语言变化而重写注释,entry 名称或 groups.


运行状态有意保存在 EXE 或所选配置文件旁边. 不要把程序放进当前用户不可写的目录.

## 命令行

默认配置:

```powershell
.\CommandTrayHostGo.exe
```

指定配置:

```powershell
.\CommandTrayHostGo.exe -c configs\work.json
.\CommandTrayHostGo.exe -c "D:\CTH Config\work.json"
```

规则:

- 相对 `-c` 路径以 EXE 最终目录为基准, 不是启动命令时的当前目录.
- 默认配置为 `<EXE directory>\config.json`.
- 默认 cache 为 `<EXE directory>\command_tray_host.cache`.
- 使用 `-c` 时, cache 为 `<config path>.cache`.
- 配置目录不存在时会自动创建.
- Start on Boot 会保留当前 `-c` 参数.
- 同一个最终物理 EXE 路径只允许一个实例. 不同目录中的副本可以同时运行.
- junction 和 symlink 会解析到最终物理路径, 不能绕过单例或产生另一套运行状态.

## 配置格式

`config.json` 支持:

- `//` 和 `/* ... */` 注释.
- trailing comma.
- UTF-8、UTF-16 和 UTF-32 输入.
- 不区分大小写的已知字段名.
- 最多 3584 个 `configs` 项.
- 配置文件大小上限为 100 MiB.

已知可选字段应省略,不能显式设为 `null`. 未知字段(包括旧根字段 `global`)被忽略,没有运行效果;拼错字段不会启用对应功能. cron 的保留字段 `need_renew` 是例外,会被明确拒绝.

Windows 路径可以使用 `/`, 避免在 JSON 字符串中转义 `\`.

### 最小示例

```jsonc
{
  // 自动更新默认开启; 示例中显式关闭.
  "lang": "auto",
  "auto_update": false,
  "enable_cache": true,
  "left_click": ["Console"],
  "configs": [
    {
      "name": "Console",
      "path": "C:/Windows/System32",
      "cmd": "cmd.exe /d /k echo CommandTrayHostGo",
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

每个 entry 必须显式包含以下字段, 即使值为空或为 `false`:

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

当前 `addition_env_path` 和 `use_builtin_console` 仅为保留字段, 没有运行时效果.

## 根配置字段

### 基础行为

|字段|默认值|说明|
|---|---:|---|
|`lang`|`auto`|空字符串或精确的 `auto` 使用下述自动检测. 精确匹配 `zh-CN`,`zh-Hans`,`zh`,`zh-SG` 时使用简体中文,其他值使用英文. 值区分大小写且不去除首尾空白.|
|`require_admin`|`false`|启动时通过 UAC 重新运行整个 CommandTrayHostGo.|
|`start_show_silent`|`true`|受管程序先隐藏启动, 再由窗口控制逻辑应用目标显示状态, 减少窗口闪烁.|
|`left_click`|`[]`|托盘左键依次切换指定 entry,通过精确匹配且区分大小写的 `configs[].name` 引用. 未知名称和数字索引均为配置错误. 空数组时切换内置 control console. entry 重命名时必须同步更新这些引用.|
|`enable_groups`|`false`|只有为 `true` 且 `groups` 字段存在时,才构造 entry 菜单层级.|
|`groups`|未设置|菜单项数组; 元素可以是精确匹配且区分大小写的 `configs[].name` 字符串或嵌套 group 对象. 未知名称和数字索引均为配置错误.|
|`groups_menu_symbol`|`+`|group 子菜单名称前缀.|
|`cmd_menu_max_length`|`0`|范围 `0..2147483647`. 最多保留 path/cmd 的前 N 个 Unicode 字符,发生截断时再追加 `...`. `0` 表示不截断.|
|`icon`|内置图标|托盘 `.ico` 文件, 同时应用到内置 control console. 相对路径以 EXE 目录为基准. 提权不会把产品图标替换为 shield icon.|
|`icon_size`|`256`|读取托盘图标时使用的尺寸, 只能是 `16`、`32` 或 `256`.|
|`configs`|必填|至少一个 entry 的数组.|

自动检测读取用户默认 locale 名称,系统默认 LCID 和系统 ANSI code page,而非 Windows 显示语言设置. 上述简体中文 locale 名称,LCID `2052`,ACP `936` 任意一项命中即使用中文,否则使用英文. 显式 `lang` 覆盖这三项检测信号.

热重载只有在候选配置提交后才切换内置菜单,提示和 About. reload 确认框使用当前语言;取消或候选失败时保留旧语言. 用户配置的 entry/group 名称不会自动翻译. Windows 原生按钮和技术错误详情不随应用文案目录切换.

### Cache 和热重载

|字段|默认值|说明|
|---|---:|---|
|`enable_cache`|见下文|启用状态 cache.|
|`conform_cache_expire`|`true`|控制配置文件 watcher. `false` 禁用热重载, 但启动时仍检查 cache 是否过期.|
|`disable_cache_position`|`false`|不保存或恢复窗口位置.|
|`disable_cache_size`|`false`|不保存或恢复窗口尺寸.|
|`disable_cache_enabled`|`true`|不保存或恢复 entry enabled 状态.|
|`disable_cache_show`|`false`|不保存或恢复窗口显示状态.|
|`disable_cache_alpha`|`false`|不保存或恢复透明度.|
|`auto_hot_reloading_config`|`false`|配置变化时不弹出确认, 自动保留兼容的运行中进程和 cache 状态并提交新配置.|

如果所有 cache 相关字段都省略, cache 默认禁用. 一旦配置了任意 cache 字段, 省略的 `enable_cache` 按 `true` 处理. 推荐显式设置 `enable_cache`.

启动时 `auto_hot_reloading_config=true` 也会自动保留并重基过期 cache,不弹出确认;其作用不限于 watcher 触发的 reload.

配置文件时间或大小发生变化时触发 reload:

- `auto_hot_reloading_config=false`: 询问是否清除状态并重启受管程序、保留兼容状态, 或取消本次 reload.
- `auto_hot_reloading_config=true`: 自动保留兼容状态.
- 新配置的 cron、图标、热键和 cache 会先完成 staging; 任何一步失败都会保留当前配置.
- 新配置要求管理员权限而当前宿主未提权时, 本次 reload 被拒绝.
- disabled entry 仍执行完整校验; `enabled=false` 不会绕过无效 cron、hotkey 或 groups 配置.
- 修改 `repeat_mod_hotkey` 需要重启 CommandTrayHostGo.

### 热键

|字段|默认值|说明|
|---|---:|---|
|`enable_hotkey`|`true`|控制全局和单项热键注册. 禁用时仍校验语法和重复绑定,但不注册系统热键.|
|`repeat_mod_hotkey`|`false`|`true` 允许按键自动重复产生多次热键事件.|
|`show_hotkey_in_menu`|`true`|在菜单文字后显示已注册热键.|
|`global_hotkey_alpha_step`|`5`|全局透明度热键每次调整的步长, 范围 `1..255`.|
|`hotkey`|空对象|全局热键映射.|

全局 `hotkey` 支持:

|字段|动作|
|---|---|
|`disable_all`|停止除 `ignore_all` 外的受管 entry.|
|`enable_all`|启动除 `ignore_all` 外的 entry.|
|`hide_all` / `show_all`|隐藏或显示所有已运行 entry 的窗口.|
|`restart_all`|重启除 `ignore_all` 外的受管 entry.|
|`elevate`|切换宿主权限: 普通权限时直接请求系统 UAC, 已提权时以原桌面用户的普通权限重启. 两个方向均恢复运行状态; 取消 UAC 不影响现有进程.|
|`exit`|退出宿主.|
|`left_click` / `right_click`|执行托盘左键或右键动作.|
|`add_alpha` / `minus_alpha`|调整当前前台窗口透明度.|
|`topmost`|切换当前前台窗口置顶状态.|
|`hide_current`|隐藏当前前台窗口并加入 Docked Windows.|
|`show_all_docked`|恢复所有 Docked Windows.|

热键语法不区分大小写,支持 `Alt`,`Ctrl`,`Shift`,`Win`,数字,字母和 `0xNN` virtual-key code. 下例是根层级片段,必须合并到包含 `configs` 的配置中:

```jsonc
{
  "hotkey": {
    "hide_all": "Ctrl+Alt+H",
    "show_all": "Ctrl+Alt+S",
    "topmost": "Win+Alt+T"
  }
}
```

特殊按键写法:

- `Ctrl++`: plus.
- `Ctrl+-`: minus.
- `Alt+ `: space.
- `Ctrl+Alt+0x26`: virtual-key code `0x26`.

同一热键不能被多个动作占用.

启动时热键校验或系统注册失败,会撤销本轮热键注册,不保留半套快捷键. 热重载注册失败则保留旧热键,撤销候选配置的新注册.

### 更新检查

|字段|默认值|说明|
|---|---:|---|
|`auto_update`|`true`|可比较的构建启动后检查 GitHub Release. 自动生成的测试配置显式设置为 `false`.|
|`skip_prerelease`|`true`|忽略 prerelease. 设为 `false` 时选择最新已签名 prerelease; 只要其 tag 与当前版本不同就安装更新.|

只接受 `v1.2.3`、`v1.2.3-rc.1` 和 `v1.2.3-dev.g<commit>` 等 SemVer tag. 每个 checker 固定绑定一个通过校验的 `owner/repository` 身份, 并只返回三种明确结果: 当前版本未知、已是最新或存在更新.

- 请求仅通过 HTTPS 访问 GitHub Releases API, 最多接受 1 MiB JSON, 并拒绝跳转到 `https://api.github.com` 之外的地址.
- draft release 和不支持的 tag 会被忽略. 每次 dev 发布都会删除旧 prerelease. `skip_prerelease=true` 排除 prerelease; 设为 `false` 时选择唯一保留的最新 prerelease, 只要 tag 与当前版本不同就更新.
- 预发布流程先完整分页获取 Release, 再删除现有 prerelease 及其 tag, 包括重跑时的同提交 tag. 正式 Release 不删除.
- 传输错误、HTTP 408/5xx 及确认属于限流的 403/429 最多重试五次. 服务端 retry header 优先, 所有等待均可通过 context 取消.
- 开发构建不会自动检查. 手动检查可以显示最新 release, 但不会声称当前版本可比较.
- 确认更新后, 客户端下载规范签名 manifest 和更新包, 校验签名及 SHA-256 摘要, 然后替换 EXE 并重启.
- 重启后的应用等待更新 helper 退出, 再删除 helper EXE 和 `.previous` 备份. 清理错误通过消息框报告.
- 安装失败时, helper 使用原配置及启动用户 SID 重启仍可用或已成功恢复的旧 EXE. 回滚失败则保留备份, 不启动未经确认的目标; 安装及恢复错误合并报告. 失败事务可能保留临时文件以便恢复.
- 每次更新在目标目录生成唯一临时 EXE, helper 和备份使用对应的独立路径. 同目录并行实例不共享更新文件, 清理只删除本次事务的文件.
- 下载和验证在后台执行. 退出或会话关闭会取消操作并丢弃结果, 已取消的下载不会自动安装. 更新交接使用继承进程句柄, 并保留指定配置和原始启动用户 SID.

## Entry 配置字段

|字段|默认值|说明|
|---|---:|---|
|`name`|必填|托盘菜单名称,console 初始标题和 cache identity. 不能为空或包含 NUL,且必须在所有 entry 中唯一,包括 disabled entry (区分大小写). entry 重命名时必须同步更新根层级 `left_click` 和所有 `groups` 引用. 唯一性要求仅适用于 `configs[].name`,不适用于 group 标题. 子程序可随后自行修改标题.|
|`path`|必填|可执行文件基准目录. 绝对路径直接使用; 相对路径以 EXE 目录为基准; 可为空.|
|`cmd`|必填|命令和参数. 可执行文件部分必须以 `.exe` 结尾. 含空格的可执行文件路径应加双引号.|
|`working_directory`|必填|空值使用 `path`; 绝对路径直接使用; 普通相对路径相对于 `path`; 以 `>` 开头时相对于 EXE 目录.|
|`addition_env_path`|必填|保留字段, 当前没有运行时效果.|
|`use_builtin_console`|必填|保留字段, 当前没有运行时效果.|
|`is_gui`|必填|`true` 按 child PID 查找 GUI 窗口, 优先使用无 owner 的标准顶层窗口, 找不到时接受 tool window 或由 CommandTrayHostGo 拥有的窗口; `false` 通过隔离 helper 关联传统 console 窗口.
|`enabled`|必填|启动时是否运行. Cache 可以按配置覆盖此状态.|
|`require_admin`|`false`|要求管理员权限. 受管模式要求先提升宿主; fully detached 模式可单独通过 UAC 启动.|
|`start_show`|见下文|启动后是否显示窗口. 受管 entry 默认 `false`; detached/job-only entry 默认 `true`.|
|`ignore_all`|`false`|不参与 Enable All、Disable All、Restart All 和同 `exclusion_id` 的批量停止.|
|`position`|未设置|`[x, y]`. `0..1` 按屏幕比例换算, 大于 `1` 按像素处理.|
|`size`|未设置|`[width, height]`. `0..1` 按屏幕比例换算, 大于 `1` 按像素处理.|
|`alpha`|未设置|窗口 alpha, 范围 `0..255`.|
|`topmost`|`false`|窗口置顶.|
|`not_host_by_commandtrayhost`|`false`|fully detached: 不加入 Job Object, 不保留进程句柄, 不提供窗口和停止控制.|
|`not_monitor_by_commandtrayhost`|`false`|job-only: 加入 Job Object, 但不保留进程句柄或提供窗口和停止控制. 同时设置两个字段时本字段优先.|
|`stop_cmd`|空|可选停止命令. 停用时先在 entry 工作目录中通过隐藏的 `cmd.exe /d /s /c` 执行, 再执行原有停止流程. 即使命令失败, 仍继续执行 `WM_CLOSE`/等待/`TerminateProcess` 或专属 Job Object 清理. 命令必须自行结束.|
|`kill_timeout`|`200`|范围 `0..4294967294` 毫秒. `kill_process_tree=false` 时等待进程退出;GUI 窗口会先收到关闭请求.|
|`kill_process_tree`|`false`|`true` 立即终止 entry 的专属 Job Object, 包括 `stop_cmd` 先结束根进程后仍存活的后代, 不等待 `kill_timeout`. `false` 等待 `kill_timeout`, 必要时再只终止根进程.|
|`exclusion_id`|未设置|范围 `1..2147483647` 的整数. 启动前停止同 ID 的其他运行中受管 entry,但跳过 `ignore_all` 项. 不允许用于 detached/job-only entry.|
|`hotkey`|空对象|当前 entry 的热键映射.|
|`crontab_config`|未设置|当前 entry 的 cron 配置.|
|`icon`|未设置|应用到目标窗口的 `.ico` 文件, 相对路径以 EXE 目录为基准.|

`position` 和 `size` 均必须恰好包含两个有限数值,各值范围为 `0..2147483647`.

`hotkey` 支持以下单项动作:

|字段|动作|
|---|---|
|`hide_show`|切换窗口显示状态.|
|`disable_enable`|停止或启动 entry.|
|`restart`|重启受管 entry.|
|`elevate`|以管理员权限启动该 entry.|

Windows Terminal 和其他 pseudoconsole 没有可由 child PID 独立控制的传统 console HWND. 此类进程仍可管理, 但不能依赖 `is_gui=false` 的窗口位置、尺寸、透明度、置顶或显示控制.

受管 entry 在隐藏启动且未配置图标、位置、尺寸、透明度或置顶状态时, 可以在没有可控制窗口的情况下运行. 只有配置或用户操作需要窗口控制时才开始窗口发现.

## 进程所有权

|配置|模式|宿主退出时|运行期间控制|
|---|---|---|---|
|两个字段均为 `false`|managed|Job Object 清理|启动、停止、重启和窗口控制|
|`not_monitor_by_commandtrayhost=true`|job-only|Job Object 清理|启动后不监控, 不提供停止和窗口控制|
|`not_host_by_commandtrayhost=true`|fully detached|保留进程|启动后不监控, 不提供停止和窗口控制|

所有普通受管进程以 suspended 状态创建, 成功加入 Job Object 后再 resume, 避免 assignment 前逃逸. Job Object 使用 `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`.

如果父 Job Object 拒绝 breakaway, Job Object 托管的子进程可通过嵌套作业启动, 仍保持宿主所有权. Fully detached 启动仍要求成功脱离, 被拒绝时会报告失败.

## Groups

`groups` 中的字符串必须精确匹配已有的 `configs[].name`,区分大小写. 未知名称和数字索引均为配置错误. group 对象必须提供 `name`,省略嵌套的 `groups` 数组时为空组. group 标题不要求全局唯一,可以重复引用同一个 entry. entry 重命名时必须同步更新所有引用. 最大嵌套深度为 40,包含 group 对象在内的菜单项总数最多 3584. 下例是根层级片段,要求 `configs` 包含名为 `Console`,`Editor` 和 `Monitor` 的 entry,不是完整配置文件:

```jsonc
{
  "enable_groups": true,
  "groups_menu_symbol": "+",
  "groups": [
    "Console",
    {
      "name": "Tools",
      "groups": ["Editor", "Monitor"]
    }
  ]
}
```

## Cron

Cron 使用本地时间和六个字段, 第一项是秒:

```text
second minute hour day-of-month month day-of-week
```

各字段范围为秒 `0..59`,分 `0..59`,时 `0..23`,日 `1..31`,月 `1..12` 或 `JAN..DEC`,星期 `0..7` 或 `SUN..SAT`;`0` 和 `7` 均表示周日. 支持 `*`,逗号列表,升序范围和正整数 `/step`. 日和星期还接受单独的 `?`,含义等同 `*`.

月,日和星期必须同时匹配(AND),不使用部分 cron 实现的日/星期 OR 规则. 在 400 年搜索范围内没有未来触发时间的表达式会被拒绝.

以下片段位于一个 managed `configs[i]` entry 内,在工作日每 5 分钟的第 0 秒执行. 不要把 `crontab_config` 放在根层级:

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

|字段|默认值|说明|
|---|---:|---|
|`enabled`|`true`|启用当前调度.|
|`crontab`|必填|六字段 cron expression, 最多 255 bytes.|
|`method`|必填|`start`、`restart`、`stop`、`start_count_stop` 或 `restart_count_stop`.|
|`count`|必填|范围 `0..2147483647`. `0` 表示无限执行;正数表示最多执行次数.|
|`start_show`|cache/`false`|cron 启动或重启后是否显示窗口. 显式值优先, 其次使用 cached show, 否则为 `false`.|
|`log`|空|日志文件. 相对路径以 EXE 目录为基准.|
|`log_level`|`0`|范围 `0..3`. 当前 level 0 记录动作结果, level 1 增加启动详情, level 2 增加调度详情; level 3 当前与 level 2 相同.|

调度的 `enabled=false` 不会跳过必填字段,表达式/范围或所有权校验. `need_renew` 是内部保留字段,不得出现在用户配置中.

日志超过 10 MiB 后轮转为 `.1`、`.2` 等文件, 最多寻找 499 个轮转目标.

`restart`、`stop` 和 count-stop 方法只允许用于 managed entry. `start_count_stop` 和 `restart_count_stop` 在计数耗尽后停止目标; 最终停止失败会继续调度重试.

## 托盘操作

右键菜单包含:

- 每个 entry 的 path、cmd、Show/Hide、Enable/Disable、Restart 和 Run As Administrator.
- Hide All 和 All 子菜单.
- Start on Boot.
- Elevate.
- Help、检查更新和 About.
- Docked Windows.
- Exit.

发现兼容的新正式版本时, 检查更新会通过 HTTPS 下载签名 manifest 和更新包, 校验 Ed25519 签名及两个 SHA-256 摘要, 然后退出、替换 EXE 并自动重启. 开发构建的版本不能安全比较, 不会自动安装更新.

单击 entry 的 path 会打开目录; 单击 cmd 会在 Explorer 中定位可执行文件.

启用 cron 的 entry 会在 path 和 cmd 行显示 check mark. 普通权限时菜单显示“提权”, 已提权时显示“取消提权”, check mark 表示当前管理员状态. 菜单和 `hotkey.elevate` 均执行双向权限切换.

受管程序运行时也可切换宿主权限, 不显示应用确认框. 提权直接请求系统 UAC; 取消提权使用原用户 Windows Explorer 的非管理员令牌重启, 不会用普通子进程启动方式继承管理员权限. 原桌面 shell 不可用或用户不匹配时安全失败. 新实例验证目标权限及配置并确认就绪, 旧宿主随后采集最终运行状态, 收到确认后才提交退出. 新实例等待旧宿主完成清理后恢复启用和显示状态, 不依赖缓存是否开启. Job Object 内的程序会重启, 已启动的 detached 程序不会重复启动. 取消 UAC 或交接失败时, 原宿主和仍在运行的子进程保持不变. 根配置 `require_admin=true` 或需要重启的 entry 明确要求管理员权限时, 会拒绝取消提权. 当前 `-c` 参数和原始启动用户 SID 保留.

提权通过经过身份验证的本地命名管道传输快照, 不使用持久化或共享可写文件; 交接不要求配置目录可写. 两个实例在接受交接前均验证对端进程身份. 交接数据按配置 entry 顺序保存运行状态标志, 并绑定原始配置内容的 SHA-256 摘要. entry 名称仅保留在配置中, 不会复制到交接数据, 因此长名称不会增大交接数据或阻止提权. 查询进程状态失败时会报告对应 entry 和原生错误, 而不是将其视为取消; 原宿主及其受管程序继续运行.

Docked Windows 标签依次回退到窗口标题、Win32 class name 和本地化无标题标签. 选择单个 docked window 会恢复并激活它; Show All 恢复全部窗口,但不会任意选择一个窗口激活.

Start on Boot 写入当前 Windows 用户的 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`. 注册项按最终 EXE 路径隔离. 以其他凭据提升后的实例不能修改原用户的 Start on Boot 状态.

- Run 项名使用完整 EXE 路径 SHA-256 的紧凑 URL-safe 编码. 打开托盘菜单或启用 Start on Boot 时, 尝试将属于当前实例的旧 `CommandTrayHost_<hash>` 注册项迁移到 `CommandTrayHostGo_<hash>`, 完整保留 Windows `StartupApproved` 状态, 包括禁用状态. 迁移错误会报告, 但不阻断菜单以及对现有注册项的查询和禁用. 命令必须匹配当前 EXE 和配置, 且当前用户必须是原始用户. 不覆盖或删除冲突的其他命令注册项. 禁用会移除新旧命名空间中属于本实例的值及审批记录. 更早的 80 字符命名格式不迁移.

## 构建

需要:

- Go 1.27.
- [`just`](https://github.com/casey/just).
- WSL2 或其他可以交叉编译 Windows amd64 Go 程序的环境.

```bash
just test
just check
just build
```

构建结果:

```text
dist/windows-amd64/CommandTrayHostGo.exe
```

构建使用:

```text
GOOS=windows
GOARCH=amd64
CGO_ENABLED=0
```

发布版本只在 `internal/app/about.go` 的 `Version` 中维护. `just build` 读取该值, 在链接前动态生成 amd64 Windows 版本、manifest 和图标资源. Explorer 的产品版本与程序内显示一致; Windows 要求的四段数字文件版本由同一值映射为 `MAJOR.MINOR.PATCH.0`. EXE 还包含产品名、原始文件名、公司、文件说明和版权信息. 构建结束后会删除生成的资源对象.

可选环境变量:

- `CTH_GOPROXY`: 覆盖 Go module proxy.
- `CTH_RELEASE_REPOSITORY`: 覆盖构建时写入的 GitHub `owner/repository`, 默认 `fcying/CommandTrayHostGo`.

## License

[MIT](LICENSE). 内置托盘和窗口图标沿用自 [rexdf/CommandTrayHost](https://github.com/rexdf/CommandTrayHost), 遵循其 MIT License. 原版权声明保留于 `LICENSE`.
