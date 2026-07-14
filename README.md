# X Downloader

X Downloader 是一个由 Chromium 扩展和本地 Go Helper 组成的 X/Twitter 视频下载工具。视频在 X 页面内识别，由本机 Helper 获取 HLS 并调用 FFmpeg 无损封装为 MP4。

它的核心原则是：媒体不上传、不读取浏览器 Cookie、不解析私有 GraphQL，下载路径和任务状态只保存在本机。

## 功能

- 在 X/Twitter 页面内检测公开 HLS 视频，同时覆盖 XHR 和 Fetch 请求。
- `列表`模式按帖子展示缩略图、分辨率和下载状态。
- `帖内`模式在帖子操作栏加入下载按钮；多视频帖子支持悬浮框多选。
- Helper 精确匹配 master 中的视频 variant 与音频 rendition，再由 FFmpeg 封装为 MP4。
- 支持队列、并发限制、取消、失败重试、完成通知和在文件管理器中显示。
- 下载时展示百分比、速度和阶段；无法取得媒体总时长时自动使用不确定进度条。
- 扩展弹窗展示 Helper、FFmpeg、下载目录、持久化和最近任务状态。
- Helper 自带本地网页管理台，可查看分页任务、搜索历史、统计、标签和修改下载配置。
- 独立设置页可调用系统文件夹选择器；下载目录立即生效并在 Helper 重启后恢复，支持外置磁盘和其他挂载卷。
- 设置页支持文件命名模板、1–4 个并发任务、0–5 次失败重试和下载总开关。
- Helper 获取 master 和 FFmpeg 下载分片时使用扩展读取的当前浏览器 User-Agent。
- 候选、任务、设置、标签和备注持久化到 SQLite，支持上万条任务、全文搜索和游标分页。
- Helper 重启后会恢复等待任务；执行中的任务在仍有尝试次数时会重新排队。
- 严格限制为回环 Helper 和 `video.twimg.com` HTTPS playlist。

## 快速开始

前置条件：Go 1.24+、Chromium/Chrome 类浏览器和 FFmpeg。

```bash
cd helper
go build -o x-downloader-helper ./cmd/x-downloader-helper
./x-downloader-helper
```

另开终端读取配对令牌：

```bash
cd helper
./x-downloader-helper -print-token
```

然后在 `chrome://extensions` 开启开发者模式，选择“加载已解压的扩展程序”，加载本仓库的 `browser-extension` 目录。打开扩展，填写令牌并点击“保存并检查”。

Helper 运行后可访问 `http://127.0.0.1:17890/` 打开网页管理台，也可点击扩展弹窗右上角的 ↗。网页中的令牌只保存在当前浏览器标签页。

完整安装、配置和使用步骤见 [用户使用说明](docs/user-guide.md)。实现边界见 [技术设计](docs/technical-design.md)，本地数据说明见 [隐私说明](docs/privacy.md)。

## 默认位置

```text
下载文件：~/Downloads/X-Media/
临时文件：~/Downloads/X-Media/.partial/
令牌：    用户配置目录/x-downloader/token
状态目录：用户配置目录/x-downloader/state/
数据库：  用户配置目录/x-downloader/state/x-downloader.sqlite3
```

下载目录、命名模板、并发数和失败重试可以直接在扩展设置页修改。也可复制 [helper/config.example.json](helper/config.example.json) 修改这些设置的默认值和 FFmpeg 路径。

从旧版首次升级时，Helper 会事务化导入 `candidates.json`、`jobs.json` 和 `settings.json`，成功后把原文件保留为 `.migrated-v1.bak`。已下载的视频不会移动。

## 项目结构

```text
browser-extension/  Chromium Manifest V3 扩展
helper/             本地 Go 服务、状态和下载队列
docs/               用户、隐私与技术文档
```

## 本地验证

```bash
cd browser-extension
npm test
npm run check

cd ../helper
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/x-downloader-helper
```

## 当前边界

- 只处理用户拥有或获准保存的公开内容，不绕过 DRM、付费或私密访问控制。
- 当前正式下载针对 master 明确关联的 H.264/AVC 视频和 AAC 音频；单文件 MP4 和其他编码仍需样本扩展。
- 轮播中的每个视频通常需要切换并播放一次，扩展才能观察到其 master。
- 百分比依赖 HLS VOD 播放列表提供可计算的总时长；无法计算时仍显示运行阶段、已处理时长和动态进度条。
- 当前提供源码构建和“加载已解压扩展”，尚未提供签名安装器、自动启动和自动更新。
