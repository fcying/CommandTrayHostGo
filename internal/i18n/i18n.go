package i18n

type Language uint8

const (
	English Language = iota
	SimplifiedChinese
)

type SystemLocale struct {
	Name string
	LCID uint32
	ACP  uint32
}

func Resolve(configured string, system SystemLocale) Language {
	if configured == "" || configured == "auto" {
		if isSimplifiedChinese(configuredAlias(system.Name)) || system.LCID == 2052 || system.ACP == 936 {
			return SimplifiedChinese
		}
		return English
	}
	if isSimplifiedChinese(configuredAlias(configured)) {
		return SimplifiedChinese
	}
	return English
}

func configuredAlias(value string) string {
	switch value {
	case "zh-Hans", "zh", "zh-SG":
		return "zh-CN"
	default:
		return value
	}
}

func isSimplifiedChinese(value string) bool {
	return value == "zh-CN"
}

type Strings struct {
	HideAll                string
	DisableAll             string
	EnableAll              string
	ShowAll                string
	RestartAll             string
	All                    string
	StartOnBoot            string
	OriginalUserOnly       string
	Elevate                string
	Home                   string
	About                  string
	Help                   string
	Exit                   string
	Show                   string
	Hide                   string
	Enable                 string
	Disable                string
	RestartCommand         string
	RunAsAdministrator     string
	DockedWindows          string
	UntitledWindow         string
	CacheExpiredPrompt     string
	ReloadPrompt           string
	ReloadRequiresAdmin    string
	WatcherStopped         string
	WindowInitFailedPrefix string
	NoControllableWindow   string
	Version                string
	Revision               string
	Author                 string
	BuildTimestamp         string
	StoppingManaged        string
	Development            string
	Unknown                string
	Modified               string
	CheckForUpdates        string
	CheckingForUpdates     string
	NoUpdates              string
	UpdateAvailable        string
	UpdateVersionUnknown   string
	UpdateCheckFailed      string
	UpdaterAlreadyRunning  string
}

func Text(language Language) Strings {
	if language == SimplifiedChinese {
		return Strings{
			HideAll:                "隐藏全部",
			DisableAll:             "全部禁用",
			EnableAll:              "全部启动",
			ShowAll:                "全部显示",
			RestartAll:             "全部重启",
			All:                    "全部",
			StartOnBoot:            "开机启动",
			OriginalUserOnly:       "仅限原用户",
			Elevate:                "提权",
			Home:                   "主页",
			About:                  "关于",
			Help:                   "帮助",
			Exit:                   "退出",
			Show:                   "显示",
			Hide:                   "隐藏",
			Enable:                 "启用",
			Disable:                "停用",
			RestartCommand:         "重启命令",
			RunAsAdministrator:     "管理员运行",
			DockedWindows:          "停泊窗口",
			UntitledWindow:         "无标题窗口",
			CacheExpiredPrompt:     "config.json 不早于 command_tray_host.cache. 是否清除已过期缓存?\n\n选择“否”将保留并应用该缓存.",
			ReloadPrompt:           "config.json 已更改. 是否重新加载?\n\n是: 清除缓存状态并重启受管程序.\n否: 保留缓存状态, 并保留配置未变的受管程序.\n取消: 继续使用当前配置.",
			ReloadRequiresAdmin:    "重新加载的配置要求管理员权限. 请重启 CommandTrayHost 以应用该配置.",
			WatcherStopped:         "配置目录监控意外停止. 请重启 CommandTrayHost 以恢复热重载.",
			WindowInitFailedPrefix: "窗口控制初始化失败:",
			NoControllableWindow:   "%s: 在 %d 秒内未找到可控制窗口",
			Version:                "版本",
			Revision:               "修订",
			Author:                 "作者",
			BuildTimestamp:         "编译时间",
			StoppingManaged:        "正在停止受管程序",
			Development:            "开发版本",
			Unknown:                "未知",
			Modified:               "已修改",
			CheckForUpdates:        "检查更新...",
			CheckingForUpdates:     "正在检查更新...",
			NoUpdates:              "当前已是最新版本.\n\n当前版本: %s\n最新版本: %s",
			UpdateAvailable:        "发现新版本 %s. 当前版本为 %s.\n\n内置更新器不会自动下载或执行发布资产, 只打开 HTTPS Release 页面. 是否立即打开?",
			UpdateVersionUnknown:   "当前开发构建版本 %s 无法与正式版本安全比较.\n\n最新可用版本: %s\n是否打开 HTTPS Release 页面?",
			UpdateCheckFailed:      "检查更新失败:",
			UpdaterAlreadyRunning:  "更新检查已在进行中.",
		}
	}
	return Strings{
		HideAll:                "Hide All",
		DisableAll:             "Disable All",
		EnableAll:              "Enable All",
		ShowAll:                "Show All",
		RestartAll:             "Restart All",
		All:                    "All",
		StartOnBoot:            "Start on Boot",
		OriginalUserOnly:       "original user only",
		Elevate:                "Elevate",
		Home:                   "Home",
		About:                  "About",
		Help:                   "Help",
		Exit:                   "Exit",
		Show:                   "Show",
		Hide:                   "Hide",
		Enable:                 "Enable",
		Disable:                "Disable",
		RestartCommand:         "Restart Command",
		RunAsAdministrator:     "Run As Administrator",
		DockedWindows:          "Docked Windows",
		UntitledWindow:         "Untitled Window",
		CacheExpiredPrompt:     "config.json is not older than command_tray_host.cache. Clear the expired cache?\n\nChoose No to keep and apply it.",
		ReloadPrompt:           "config.json changed. Reload it?\n\nYes: clear cached state and restart managed programs.\nNo: keep cached state and preserve unchanged managed programs.\nCancel: keep the current configuration.",
		ReloadRequiresAdmin:    "The reloaded config requires administrator privileges. Restart CommandTrayHost to apply it.",
		WatcherStopped:         "Configuration directory monitoring stopped unexpectedly. Restart CommandTrayHost to resume hot reload.",
		WindowInitFailedPrefix: "Window control initialization failed:",
		NoControllableWindow:   "%s: no controllable window found within %d seconds",
		Version:                "Version",
		Revision:               "Revision",
		Author:                 "Author",
		BuildTimestamp:         "Build Timestamp",
		StoppingManaged:        "Stopping managed programs",
		Development:            "development",
		Unknown:                "unknown",
		Modified:               "modified",
		CheckForUpdates:        "Check for Updates...",
		CheckingForUpdates:     "Checking for Updates...",
		NoUpdates:              "CommandTrayHost is up to date.\n\nCurrent version: %s\nLatest version: %s",
		UpdateAvailable:        "Version %s is available; the current version is %s.\n\nThe built-in updater does not download or execute release assets; it only opens the HTTPS Releases page. Open it now?",
		UpdateVersionUnknown:   "Development build %s cannot be compared safely with formal releases.\n\nLatest available version: %s\nOpen the HTTPS Releases page?",
		UpdateCheckFailed:      "Update check failed:",
		UpdaterAlreadyRunning:  "An update check is already running.",
	}
}
