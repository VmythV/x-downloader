# X Downloader

一个由浏览器插件和本地 Go helper 组成的 X/Twitter HLS 媒体下载工具。

## 目录

```text
browser-extension/  Chromium Manifest V3 插件
helper/             本地 Go 服务
docs/               架构和技术方案
```

## 当前状态

- 插件已实现 XHR 主世界拦截，能识别 HLS master playlist 并强制选择最高画质。
- 插件会关联帖子 DOM，只展示当前标签页、当前 X 路由捕获的视频；可在“列表”和“帖内”两种显示模式间切换。
- 列表模式按帖子分组，默认显示五张卡片并支持托盘内滚动；帖内模式在帖子收藏、分享操作栏右侧提供最高画质下载按钮，多视频帖子可弹出缩略图列表并多选下载。
- 插件弹窗可以启动限定当前 X 标签页的 HLS 诊断会话。
- Go helper 已实现令牌认证、m3u8 探测、master 展开、音视频配对和诊断报告。
- 下载任务支持并发限制、去重、取消、FFmpeg 进度、有意义的命名和临时文件原子落盘。

完整方案见 [docs/technical-design.md](docs/technical-design.md)。
诊断操作见 [docs/diagnostic-guide.md](docs/diagnostic-guide.md)。
下载实测见 [docs/download-guide.md](docs/download-guide.md)。

## 本地验证

```bash
cd browser-extension
npm test
npm run check

cd ../helper
go test ./...
go build -o x-downloader-helper ./cmd/x-downloader-helper
```

## 加载插件

1. 打开 `chrome://extensions`。
2. 开启“开发者模式”。
3. 选择“加载已解压的扩展程序”。
4. 选择本仓库的 `browser-extension` 目录。
