# X Downloader 技术设计

## 1. 目标与边界

浏览器扩展观察 X 播放器实际取得的 HLS master playlist，本地 Go Helper 重新获取并解析 master，按声明精确配对视频与音频，再调用 FFmpeg 无损封装为 MP4。

项目不使用 yt-dlp，不解析 X 私有 GraphQL，不读取浏览器 Cookie，不绕过 DRM、付费或私密访问控制。

## 2. 组件职责

```text
browser-extension/
├── options/            下载目录、连接与浏览器偏好设置
├── popup/              Helper 就绪状态、连接设置和最近任务
└── src/
    ├── hls-core.js     master 解析、质量排序和播放列表重写
    ├── page-main.js    MAIN world XHR/Fetch 观察
    ├── tray-core.js    路由过滤、帖子分组和帖内状态
    ├── content.js      DOM 关联、托盘、帖内控件和任务同步
    └── background.js   Helper API、版本检查、超时、通知

helper/internal/
├── auth/               配对令牌
├── config/             本地配置
├── downloadpath/       下载目录规范化和可写性校验
├── folderpicker/       macOS、Windows、Linux 原生目录选择
├── hls/                master 解析和 URL 校验
├── httpapi/            回环 HTTP API 与就绪状态
├── jobs/               持久化下载队列、FFmpeg、文件定位
├── media/              持久化媒体候选
├── settings/           运行时设置和目录持久化
└── statefile/          原子 JSON 状态文件
```

插件不能直接执行本地程序，Helper 也不能监听浏览器内部 HTTPS 响应。因此扩展只提交已经观察到的 master URL 和页面上下文；所有网络 URL、输出路径和 FFmpeg 参数都由 Helper 重新校验或生成。

## 3. 下载数据流

```text
X 播放器请求 master
  → MAIN world 从 XHR 响应或 Fetch clone 识别 master
  → content script 根据封面 mediaId 与 article 关联帖子
  → service worker 携带 bearer token 和浏览器 User-Agent 注册候选
  → Helper 获取原始 master，解析 variant 和 audio group
  → 页面托盘或帖内按钮让用户选择
  → Helper 持久化任务并调用 FFmpeg
  → 扩展按任务列表同步状态并发送完成/失败通知
```

XHR master 会改写为最高画质供播放器使用；Fetch 响应只通过 clone 观察，不替换原始 Response，以免改变页面的响应语义。

## 4. HLS 与安全校验

响应包含 `#EXT-X-STREAM-INF` 才视为 master。variant 按像素数、短边和 `AVERAGE-BANDWIDTH`/`BANDWIDTH` 排序。每个 variant 只关联其 `AUDIO="group-id"` 指向的音频 rendition。

master、相对解析后的子 playlist 和重定向目标都必须满足：

- `https` 协议。
- 主机严格为 `video.twimg.com`。
- 路径以 `.m3u8` 结尾。
- 不包含用户信息或自定义端口。

内容页面不能为单个下载任务提交输出目录、文件名、FFmpeg 参数或非候选媒体 URL。只有通过配对认证的扩展设置页可以更新全局下载目录与下载规则，Helper 会验证目录、命名模板、并发数和重试上限。

扩展 service worker 从 `navigator.userAgent` 读取当前浏览器 UA，限制长度并拒绝换行后提交。Helper 再次校验控制字符，将其用于 Go 获取 master 的请求，并在 FFmpeg 的视频和音频两个输入前分别设置 `-user_agent`。Cookie 和浏览器 Authorization 不会传给 Helper。

## 5. 页面上下文与交互

content script 从 `video poster` 或缩略图 URL 提取 mediaId，向上查找 `article`，再从带 `<time>` 的 `/status/{postId}` 链接取得作者、帖子和时间。

候选只展示在亲自捕获它的标签页，并按当前 X 路由过滤。两种显示模式保存在扩展本地存储：

- `列表`：按帖子分组，允许选择分辨率、下载、取消和重试。
- `帖内`：单视频一键最高画质；多视频使用挂载在页面根节点的 fixed 悬浮框多选。

待注册候选会保留错误并定时重试，也可点击“重试连接”。托盘显示 Helper 在线状态和已检测数量，提醒用户逐个播放轮播视频。

为控制长时间浏览信息流的占用，单标签页最多保留 250 个候选、250 个原始捕获和 500 个任务。

## 6. Helper API

`GET /v1/health` 是公开的轻量存活检查；其他接口都要求 `Authorization: Bearer {token}`。

```text
GET    /v1/health
GET    /v1/status

GET    /v1/settings
PUT    /v1/settings
POST   /v1/settings/pick-download-directory

POST   /v1/candidates
GET    /v1/candidates
GET    /v1/candidates/{id}

POST   /v1/jobs
GET    /v1/jobs
GET    /v1/jobs/{id}
DELETE /v1/jobs/{id}
POST   /v1/jobs/{id}/reveal
```

`/v1/status` 返回 `apiVersion`、Helper 版本、FFmpeg 状态、下载目录可写性、代理、并发数、重试次数、持久化能力、候选数量和任务统计。当前 API 版本为 3；扩展要求精确匹配。普通 Helper 请求有 20 秒超时，等待用户操作的文件夹选择请求允许 5 分钟。

设置页不依赖浏览器暴露本地绝对路径，而是让 Helper 调用操作系统目录选择器。macOS 使用 AppleScript，Windows 使用 `FolderBrowserDialog`，Linux 依次尝试 `zenity` 和 `kdialog`。选中的目录、命名模板、并发数和失败重试次数经用户确认后写入 `stateDir/settings.json`。

## 7. 下载队列与持久化

并发数允许运行时在 1–4 之间调整，队列容量为 100。降低并发时现有任务继续执行，新任务等待活动数降到限制以内；提高并发时等待任务立即获得执行槽位。相同候选与 variant 的活动任务会去重；成功文件仍存在时重复提交直接返回已完成任务。

每个新任务记录创建时的最大尝试次数。FFmpeg 或网络失败后按递增延迟重新排队，最多自动重试 0–5 次；取消、输出移动失败和 Helper 重启中断不会自动重试。

FFmpeg 使用参数数组启动，不经过 shell：

```text
ffmpeg -hide_banner -nostdin -loglevel warning
  -progress pipe:1 -nostats
  -user_agent BROWSER_UA -i VIDEO_M3U8
  -user_agent BROWSER_UA -i AUDIO_M3U8
  -map 0:v:0 -map 1:a:0
  -c copy -movflags +faststart
  -y TEMP_FILE.part.mp4
```

取消会终止整个进程组；成功后在同一文件系统内原子移动临时文件。失败和取消清理临时文件。

候选、任务和运行时设置使用 0600 权限的原子 JSON 文件保存。Helper 保留最近 300 个候选和 500 个任务。重启时残留的 `queued/downloading` 会转换为 `failed` 并标记为被重启中断，允许用户重试。

## 8. 默认配置

```json
{
  "listenAddress": "127.0.0.1:17890",
  "downloadDir": "~/Downloads/X-Media",
  "tempDir": "~/Downloads/X-Media/.partial",
  "stateDir": "~/Library/Application Support/x-downloader/state",
  "tokenFile": "~/Library/Application Support/x-downloader/token",
  "ffmpegPath": "ffmpeg",
  "concurrency": 1,
  "retryCount": 1,
  "filenameTemplate": "{date}_{author}_{postId}_{mediaIndex}_{mediaId}_{height}p.{ext}"
}
```

文件名模板支持 `{date}`、`{author}`、`{postId}`、`{mediaIndex}`、`{mediaId}`、`{width}`、`{height}` 和 `{ext}`。路径分隔符被拒绝，DOM 元数据会清理并限制长度。配置中的下载规则是设置页的默认值；设置页保存过后，持久值优先，直到用户恢复默认。

## 9. 代理

Helper 的 Go HTTP transport 读取 `HTTP_PROXY`、`HTTPS_PROXY` 和 `NO_PROXY`；FFmpeg 继承 Helper 环境。服务始终绑定 `127.0.0.1`，本地 API 应通过 `NO_PROXY=127.0.0.1,localhost` 绕过代理。

## 10. 当前限制

- 正式下载依赖 master 中明确关联的视频和音频。
- 当前主要覆盖 H.264/AVC + AAC HLS；单文件 MP4、其他编码和没有 master 的媒体仍需样本扩展。
- X DOM 和请求方式可能变化，需要浏览器端集成测试持续验证。
- 暂无签名安装器、自动启动和自动更新；FFmpeg 路径仍需通过配置文件调整。

## 11. 验证

- JavaScript 覆盖 master 解析、质量选择、重写、Fetch 观察、目录设置和帖内状态。
- Go 覆盖 URL 限制、音轨配对、API、目录持久化、原子状态文件、候选/任务恢复和下载命名。
- CI 前建议执行扩展测试、Go 全量测试、`go vet` 和 `go test -race ./...`。
