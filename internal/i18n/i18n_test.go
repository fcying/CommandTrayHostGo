package i18n

import "testing"

func TestResolve(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		system     SystemLocale
		want       Language
	}{
		{name: "explicit zh-CN", configured: "zh-CN", want: SimplifiedChinese},
		{name: "explicit zh-Hans", configured: "zh-Hans", want: SimplifiedChinese},
		{name: "explicit zh", configured: "zh", want: SimplifiedChinese},
		{name: "explicit zh-SG", configured: "zh-SG", want: SimplifiedChinese},
		{name: "explicit English ignores ACP", configured: "en-US", system: SystemLocale{ACP: 936}, want: English},
		{name: "traditional Chinese is English", configured: "zh-TW", system: SystemLocale{ACP: 936}, want: English},
		{name: "case sensitive locale", configured: "ZH-CN", system: SystemLocale{ACP: 936}, want: English},
		{name: "case sensitive auto", configured: "AUTO", system: SystemLocale{ACP: 936}, want: English},
		{name: "untrimmed auto", configured: " auto ", system: SystemLocale{ACP: 936}, want: English},
		{name: "empty auto locale", system: SystemLocale{Name: "zh-CN"}, want: SimplifiedChinese},
		{name: "auto alias", configured: "auto", system: SystemLocale{Name: "zh-Hans"}, want: SimplifiedChinese},
		{name: "auto system LCID", configured: "auto", system: SystemLocale{Name: "en-US", LCID: 2052, ACP: 1252}, want: SimplifiedChinese},
		{name: "auto ACP", configured: "auto", system: SystemLocale{Name: "en-US", LCID: 1033, ACP: 936}, want: SimplifiedChinese},
		{name: "auto English", configured: "auto", system: SystemLocale{Name: "en-US", LCID: 1033, ACP: 1252}, want: English},
		{name: "auto traditional Chinese", configured: "auto", system: SystemLocale{Name: "zh-TW", LCID: 1028, ACP: 950}, want: English},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Resolve(test.configured, test.system); got != test.want {
				t.Fatalf("Resolve(%q, %+v) = %v, want %v", test.configured, test.system, got, test.want)
			}
		})
	}
}

func TestCatalog(t *testing.T) {
	english := Text(English)
	chinese := Text(SimplifiedChinese)
	if english.RestartCommand != "Restart Command" || english.DockedWindows != "Docked Windows" {
		t.Fatalf("English catalog = %+v", english)
	}
	if chinese.EnableAll != "全部启动" || chinese.Disable != "停用" || chinese.RestartCommand != "重启命令" || chinese.DockedWindows != "停泊窗口" {
		t.Fatalf("Chinese catalog = %+v", chinese)
	}
	if chinese.Development != "开发版本" || chinese.Unknown != "未知" || chinese.Modified != "已修改" {
		t.Fatalf("Chinese metadata catalog = %+v", chinese)
	}
	if english.CheckForUpdates != "Check for Updates..." || chinese.CheckForUpdates != "检查更新..." || chinese.UpdaterAlreadyRunning != "更新检查已在进行中." {
		t.Fatalf("update catalog = English:%+v Chinese:%+v", english, chinese)
	}
}
