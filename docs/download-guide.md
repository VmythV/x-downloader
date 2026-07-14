# 下载功能实测指南

## 1. 前置条件

- Go 1.24 或更高版本。
- Chromium/Chrome 类浏览器。
- FFmpeg 可在终端执行，或在配置中填写其绝对路径。

先检查 FFmpeg：

```bash
ffmpeg -version
```

如果使用 Homebrew 且尚未安装：

```bash
brew install ffmpeg
```

当前开发机执行检查时未找到 `ffmpeg`；在安装或配置正确路径前，HLS 诊断可以工作，但下载任务会失败并在托盘显示错误。

## 2. 可选配置

默认下载到 `~/Downloads/X-Media`。如需修改，先复制示例：

```bash
cd helper
cp config.example.json config.json
```

编辑 `config.json` 中的 `downloadDir`、`ffmpegPath`、`concurrency` 或 `filenameTemplate`。网页和扩展不能修改本地输出目录。

## 3. 构建 helper

```bash
cd helper
go test ./...
go build -o x-downloader-helper ./cmd/x-downloader-helper
```

若使用默认配置，直接启动：

```bash
./x-downloader-helper
```

若创建了 `config.json`：

```bash
./x-downloader-helper -config ./config.json
```

### X 需要代理时

helper 必须能独立访问 `video.twimg.com`。以本地 HTTP 代理端口 `7890` 为例：

```bash
export http_proxy=http://127.0.0.1:7890
export https_proxy=http://127.0.0.1:7890
export HTTP_PROXY=http://127.0.0.1:7890
export HTTPS_PROXY=http://127.0.0.1:7890
export NO_PROXY=127.0.0.1,localhost
export no_proxy=127.0.0.1,localhost
./x-downloader-helper -config ./config.json
```

FFmpeg 由 helper 启动并继承同一环境，所以不要在另一个没有代理变量的终端重启 helper。若本地代理不是 `7890`，替换为实际端口。

## 4. 配对插件

在另一个终端读取令牌：

```bash
cd helper
./x-downloader-helper -print-token
```

然后：

1. 打开 `chrome://extensions`。
2. 对 X Media Downloader 点击“重新加载”。
3. 刷新已打开的 X 页面；MAIN world 脚本必须在页面加载开始时安装。
4. 打开扩展弹窗，填入 `http://127.0.0.1:17890` 和令牌。
5. 点击“保存并测试”，确认地址和配对令牌都可用。

## 5. 下载一个或多个视频

1. 在 X 页面播放目标视频，等待它请求 master playlist。
2. 页面右下角出现 X Downloader 托盘。
3. 一帖多视频时，依次切换并播放每个视频；每个 mediaId 会形成独立卡片。
4. 在卡片中选择所需分辨率。音频码率来自该 variant 在 master 中明确关联的 audio group。
5. 点击“下载”。托盘会显示等待、下载时长和速度、完成文件名，或具体失败原因。
6. 下载中可以点击“取消”。

托盘只展示当前标签页、当前 X 路由中捕获的视频，并按帖子分组。默认可见五张视频卡片；数量更多时把鼠标放在托盘内滚动。切换到另一个 X 路由后，上一页的卡片会隐藏，返回原路由时可在同一标签页会话内恢复显示。

托盘标题栏提供两种显示模式：

- `列表`：保持上述分组托盘，适合总览和连续处理多个视频。
- `帖内`：在帖子收藏和分享操作栏最右侧显示一个下载图标。单视频帖子点击后直接下载最高画质；多视频帖子会在按钮附近打开独立悬浮框，列出带缩略图的视频列表，可多选后批量下载各自的最高画质。悬浮框不受帖子容器裁剪，并会根据视口空间自动向上或向下展开。

显示模式保存在扩展本地存储中，新打开或刷新 X 页面时会沿用上一次选择。帖内模式下右下角仍保留一个小型切换条，随时可以返回列表模式。

默认文件位于：

```text
~/Downloads/X-Media/
```

命名示例：

```text
2026-07-13_someone_123456789_01_2076268346560196608_720p.mp4
```

## 6. 常见问题

### 托盘没有出现

- 确认扩展重新加载后又刷新了 X 页面。
- 主动点击播放，轮播中的每个视频都需要触发自己的 master 请求。
- 用扩展弹窗重新执行 HLS 诊断，确认 `masterDetected` 为 true。

### 帖内模式中帖子没有下载按钮

- 先播放该视频，尚未请求 master 的轮播项不会生成下载候选。
- 等待 X 完成帖子操作栏 DOM 切换；扩展会自动重新挂载按钮。
- 如果仍未出现，切回列表模式查看该 mediaId 是否已被识别以及是否有 helper 错误。

### 卡片显示“helper 未就绪”

- 检查 helper 是否仍在运行。
- 检查插件令牌和端口。
- 若错误涉及 `video.twimg.com` 连接或超时，按第 3 节带代理重启 helper。

### 下载提示找不到 FFmpeg

- 执行 `ffmpeg -version`。
- 若 FFmpeg 不在 PATH，把 `config.json` 的 `ffmpegPath` 改为可执行文件绝对路径，然后重启 helper。

### helper 重启后旧卡片不能继续操作

候选和任务状态目前保存在内存中。刷新 X 页面并重新播放视频即可注册；已经完成的 MP4 不会被删除。

### helper 终端日志包含哪些信息

启动时会显示监听地址、下载与临时目录、并发数、是否配置代理以及 FFmpeg 探测结果。运行时会显示候选媒体 ID、variant 数量、API 写操作，以及下载任务的排队、开始、复用、取消、失败和完成状态。轮询类 GET 请求与诊断 observation 批次在成功时不打印，避免终端被重复日志淹没；令牌和完整视频 URL 不会写入日志。

### 页面播放是最高画质，但下载列表有多个清晰度

这是预期行为。插件只把 X 播放器使用的 master 响应改写为最高画质；helper 会解析未改写的原始 master，并保留所有可选清晰度供用户下载。
