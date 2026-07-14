# X Downloader

X Downloader 是一个由 Chromium 扩展和本地 Go Helper 组成的 X/Twitter 视频下载工具。视频在 X 页面内识别，由本机 Helper 获取 HLS 并调用 FFmpeg 无损封装为 MP4。

它的核心原则是：媒体不上传、不读取浏览器 Cookie、不解析私有 GraphQL，下载路径和任务状态只保存在本机。

## 功能

- 在 X/Twitter 页面内检测公开 HLS 视频，同时覆盖 XHR 和 Fetch 请求。
- `列表`模式按帖子展示缩略图、分辨率和下载状态。
- `帖内`模式在帖子操作栏加入下载按钮；多视频帖子支持悬浮框多选。
- Helper 精确匹配 master 中的视频 variant 与音频 rendition，再由 FFmpeg 封装为 MP4。
- 支持队列、并发限制、取消、失败重试、完成通知和在文件管理器中显示。
- 扩展弹窗展示 Helper、FFmpeg、下载目录、持久化和最近任务状态。
- 独立设置页可调用系统文件夹选择器；下载目录立即生效并在 Helper 重启后恢复。
- 候选和任务会持久化；Helper 重启时未完成任务会标为可重试。
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

完整安装、配置和使用步骤见 [用户使用说明](docs/user-guide.md)。实现边界见 [技术设计](docs/technical-design.md)，本地数据说明见 [隐私说明](docs/privacy.md)。

## 默认位置

```text
下载文件：~/Downloads/X-Media/
临时文件：~/Downloads/X-Media/.partial/
令牌：    用户配置目录/x-downloader/token
状态：    用户配置目录/x-downloader/state/
应用设置：用户配置目录/x-downloader/state/settings.json
```

下载目录可以直接在扩展设置页选择。也可复制 [helper/config.example.json](helper/config.example.json) 修改默认下载目录、FFmpeg 路径、并发数和文件名模板。

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
- 当前提供源码构建和“加载已解压扩展”，尚未提供签名安装器、自动启动和自动更新。
