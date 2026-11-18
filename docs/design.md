# SwiftDL fnOS 版 — 设计说明

把 [harshsinghsv/swiftdl](https://github.com/harshsinghsv/swiftdl)(Rust + Tauri 桌面下载器)
改造成飞牛 fnOS 的原生 fpk 应用:**HTTP 多连接分段下载 + BT/磁力**,Web 界面经 fnOS 统一网关访问。

**技术栈(应用户要求)**:后端 Go(参考上游 Rust 引擎语义重写),前端 React + Vite + TypeScript。

## 形态

- fpk 双架构包(arm64 / x86),后端为 `CGO_ENABLED=0` 的静态 Go 二进制 `swiftdl-server`,
  走 `${TRIM_APPDEST}/app.sock` + 网关前缀 `/app/swiftdl`(同 speedtest/yejian 模式)。
- 前端 React 单页应用,同上游视觉风格(移植 styles.css,类名不变)。

## 与上游的差异

| 上游(fnOS 版不做) | 替代/说明 |
| --- | --- |
| Rust 引擎 | Go 重写:探测/分段/偷半段再平衡/秒级断点续传/镜像多址,单测覆盖 |
| Tauri 壳、托盘、独立下载窗口 | React 单页 Web UI |
| 原生 TS UI | React 组件重写,复用上游样式 |
| 浏览器扩展、New download 弹窗 | 页面内"新建下载"对话框;种子选文件为页内弹窗 |
| yt-dlp 视频站下载、debrid、FTP | 不含 |
| 原生文件夹选择器 | 服务端目录浏览 API + 页内选择器(`/vol*` 起) |
| 自动更新 / 睡眠关机 / 下载完成后关机 | 不适用(fnOS 应用由系统管理) |
| 下载完成后自动解压(whenDone/extractArchives) | 不含,完成后仅 SHA-256 校验与浏览器通知 |
| 浏览器扩展抓取(grab_page 仅探测) | 页内"页面找文件"未移植,扩展命令给桩 |
| 英文界面 | 界面文案中文 |

保留核心:多连接(≤32)分段下载、动态再平衡、断点续传、镜像多址、
队列与并发、全局限速、分类归档、SHA-256 校验、Metalink、BT(做种策略 stop/ratio/forever)。
BT 引擎用 `anacrolix/torrent`(Go 生态标准库):磁力 + .torrent、文件勾选、做种控制。

## 协议(关键决策)

前端命令协议与上游 Tauri 命令**同名同形**(camelCase):

- `POST /app/swiftdl/api/invoke` — body `{cmd, args}`,回 `{ok,data}` 或 `{ok:false,error}`
- `GET  /app/swiftdl/api/state?v=<n>` — 轮询态:带版本号,无变化回 `{same:true}`,前端 500ms 一拍
  (不用 WebSocket:fnOS 网关不保证代理)
- 桌面专属命令给桩(`is_media_url`→false、`check_update`→null 等)

## 目录与数据

- **下载目录由用户主动授权**(照 hwbench 的 apiscope 模式):不建应用私有共享,
  `config/resource` 声明 api-scope(`trim.file.userAccess` 等)但 data-share 为空。
  服务端经 `/var/run/trim_open_gateway_apiscope.socket` + `TRIM_API_TOKEN` 实时查
  `getUserAccessibleFolders`(uid 取网关注入的 `X-Trim-Userid` 头);目录浏览器只允许
  在授权文件夹里走,未授权/被收回时 fail-closed 并引导去 fnOS 应用设置授权。
  没选目录前 `add_download` 直接拒绝。
- 设置与任务列表存 `${TRIM_PKGVAR}/downloads.json` / `settings.json`(字段与上游兼容的子集)。
- 断点状态在 `<目标文件>.swiftdl`(缺失区间列表,JSON,每秒保存);上传的种子文件
  存 `${TRIM_PKGVAR}/torrent-files/`。

## 构建

- Go 1.2x,`GOOS=linux GOARCH={amd64,arm64} CGO_ENABLED=0` 直接交叉编译;
- React UI:Vite 构建,`base: './'`,产物进 fpk 的 `app/www`;
- `fnpack build` 出 `dist/swiftdl_v*_{arm,x86}.fpk`。

## 源码布局

```
src/server/   Go 后端:main/engine/limiter/manager/torrent/api/files/metalink/grabber/batch
ui/           React 前端(构建产物 ui/www)
package/      fpk 包目录(manifest/cmd/config/wizard/app + build.sh)
dist/         打包产物 swiftdl_v*_{arm,x86}.fpk
docs/ref/     上游关键源码摘录(参考;上游克隆在 /tmp/swiftdl-upstream)
```
