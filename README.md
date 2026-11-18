# SwiftDL 下载(fnOS 版)

多连接并行下载器,飞牛 fnOS 原生 fpk 应用。基于
[harshsinghsv/swiftdl](https://github.com/harshsinghsv/swiftdl)(Rust + Tauri 桌面版)
移植:后端改为 Go,前端改为 React,界面全中文。

## 功能

- **HTTP/HTTPS 多线程下载**:每个任务最多 32 条连接写入同一预分配文件,
  闲置连接自动窃取最大剩余分段(动态再平衡),下载器之间互不干扰
- **断点续传**:秒级保存缺失区间(`.swiftdl` 状态文件),暂停/失败/重启后接着下
- **镜像多址**:同一文件多个 URL 共同分担(Metalink 或手动添加)
- **BitTorrent**:磁力链接与 .torrent、页内勾选要下的文件、做完种自动停
  (按分享率 1.0 / 2.0 或一直做种),断点信息落盘,重启续传
- **ed2k(电驴)**:粘贴 ed2k:// 链接即下,自动连公共服务器与 KAD 找源
- **批量链接**:一次粘贴多个 URL;`[01-50]` 编号模式自动展开
- **分类归档**:视频 / 音乐 / 程序 / 压缩包等各归各文件夹(可关)
- **SHA-256 校验**:下载完成自动核对(可选填校验和)
- **队列与并发**:同时进行的任务数可配,排队可置顶;全局限速;定时开始
- **速度曲线**:侧栏总速度 + 任务详情页单任务曲线,连接车道图

## 安装

fnOS 应用中心手动安装 `dist/swiftdl_v*_<arch>.fpk`(arm64 机器装 `_arm`,
x86 机器装 `_x86`)。

**下载目录需要你主动授权**:应用不占用任何默认位置——安装后先在
fnOS「应用中心 → SwiftDL 下载 → 权限/授权」里把要存放下载的共享文件夹
授权给本应用,然后在 SwiftDL 设置里选定它,就能开始下载了。

## 从源码构建

```bash
package/build.sh        # 前端 + 双架构 Go 交叉编译 + fnpack
```

产物在 `dist/`。依赖:Go ≥ 1.22、Node ≥ 18、[fnpack](`~/.local/bin/fnpack`)。

## 布局

| 目录 | 内容 |
| --- | --- |
| `src/server/` | Go 后端:HTTP 引擎、BT、任务管理、API(Unix socket) |
| `ui/` | React 前端(Vite 构建,产物 `ui/www`) |
| `package/` | fpk 包目录(manifest / cmd / config / wizard / app)+ build.sh |
| `docs/` | 设计说明(协议、数据落盘位置、与上游的差异) |

设计细节(协议、与上游的差异、数据落盘位置)见 [docs/design.md](docs/design.md)。

## 上游致谢

- [harshsinghsv/swiftdl](https://github.com/harshsinghsv/swiftdl) — 引擎语义与界面风格
- [anacrolix/torrent](https://github.com/anacrolix/torrent) — Go 的 BT 协议库
- [monkeyWie/goed2k](https://github.com/monkeyWie/goed2k) — Go 的 eDonkey 协议库
