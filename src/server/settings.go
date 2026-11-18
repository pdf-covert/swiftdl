// 设置:上游字段的 NAS 版子集(camelCase 兼容)。桌面专属项(托盘、剪贴板
// 监视、aria2 API、debrid)不进 fnOS 版。

package main

// Settings 与 settings.json 一一对应。JSON 标签沿用上游命名。
type Settings struct {
	// 空表示尚未初始化,启动时填入默认下载目录。
	DownloadDir string `json:"downloadDir"`
	// 每个任务的连接数。
	Connections int `json:"connections"`
	// 同时运行的任务数,其余排队。
	MaxConcurrent int `json:"maxConcurrent"`
	// 全局限速 KiB/s,0 = 不限。
	SpeedLimitKib int64 `json:"speedLimitKib"`
	// 按类型存进 Downloads/Video、Downloads/Music… 子目录。
	SortIntoCategories bool `json:"sortIntoCategories"`
	// 浏览器扩展捕获的下载先弹确认框。
	AskBeforeDownload bool `json:"askBeforeDownload"`
	// 用户关掉了“连接浏览器”卡片。
	OnboardingDismissed bool `json:"onboardingDismissed"`
	// "system"、"light" 或 "dark"。
	Theme string `json:"theme"`
	// BT 做种策略:下载完成后 "stop",或分享率 "1"/"2",或 "forever"。
	SeedUntil string `json:"seedUntil"`
	// 到此时刻(epoch 毫秒)前压着队列;0 = 立即。
	StartAt int64 `json:"startAt"`
	// 下载代理,如 "http://127.0.0.1:8080";空 = 直连。重启后生效。
	Proxy string `json:"proxy"`
	// 界面缩放百分比(90-150)。
	UIScale int `json:"uiScale"`
}
