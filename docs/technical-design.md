# X Downloader 技术方案

## 1. 目标与边界

本项目不使用 yt-dlp，也不解析 X 的私有 GraphQL。浏览器插件观察 X 播放器实际取得的 HLS master playlist；本地 Go helper 重新获取并解析 master，按其声明精确配对视频与音频，再调用 FFmpeg 无损封装为 MP4。

项目只处理用户拥有或获准保存的公开内容，不读取浏览器 Cookie，不绕过 DRM、付费或私密访问控制。

## 2. 目录与职责

```text
x-downloader/
├── browser-extension/              Chromium Manifest V3 插件
│   ├── manifest.json
│   ├── popup/                      helper 配对与 HLS 诊断界面
│   ├── src/
│   │   ├── hls-core.js             master 解析、最高画质选择和重写
│   │   ├── page-main.js            MAIN world XHR 拦截
│   │   ├── tray-core.js            当前页面过滤与帖子分组
│   │   ├── content.js              帖子关联、媒体托盘和页面消息桥
│   │   └── background.js           helper API、诊断和会话状态
│   └── tests/
├── helper/                         Go 本地服务
│   ├── cmd/x-downloader-helper/
│   └── internal/
│       ├── auth/                   配对令牌
│       ├── capture/                playlist 捕获诊断
│       ├── config/                 本地配置
│       ├── hls/                    master 解析和子 URL 校验
│       ├── httpapi/                回环 HTTP API
│       ├── jobs/                   下载队列、进度和 FFmpeg 进程
│       └── media/                  候选媒体及页面上下文
└── docs/
```

插件不能直接执行本地程序，helper 也不能监听浏览器内部的 HTTPS 请求。因此插件只提交已观察到的 master URL 和页面上下文；所有网络 URL、文件路径与进程参数都由 helper 再次校验或生成。

## 3. 数据流

```text
X 播放器请求 master playlist
  → MAIN world 拦截 XHR 响应
  → 记录原始 master 信息并仅保留最高画质 variant 供播放器使用
  → content script 从封面和 article DOM 关联帖子上下文
  → service worker 携带 bearer token 提交 master URL
  → helper 重新获取并解析原始 master
  → 形成一个 mediaId 候选及多组“视频 variant + 精确音频 rendition”
  → 网页右下角托盘展示候选，由用户选择清晰度
  → helper 排队并用 FFmpeg 合并为 MP4
  → 托盘轮询并显示等待、下载、完成、失败或取消状态
```

## 4. 最高画质播放

### 4.1 拦截位置

X 播放器在页面主 JavaScript 环境使用 `XMLHttpRequest`。Manifest V3 的普通 content script 位于隔离世界，无法修改页面自己的 XHR prototype，因此 `hls-core.js` 和 `page-main.js` 以 `world: "MAIN"`、`document_start` 运行。

### 4.2 master 判定与选择

响应包含 `#EXT-X-STREAM-INF` 才视为 master；普通 media playlist 不改写。variant 按以下顺序比较：

1. `width × height` 像素数。
2. 像素数相同时比较短边。
3. 分辨率相同时比较 `AVERAGE-BANDWIDTH`，缺失时使用 `BANDWIDTH`。

改写只移除未选中的 `#EXT-X-STREAM-INF + URI`，保留全局标签和全部 `#EXT-X-MEDIA`。被选中的视频仍可通过 `AUDIO` group 引用原 master 中的独立音轨。

## 5. master、视频与音频配对

真实诊断已经确认同一媒体通常具有以下请求：

```text
/amplify_video/{mediaId}/pl/{masterHash}.m3u8?...
/amplify_video/{mediaId}/pl/avc1/{width}x{height}/{hash}.m3u8
/amplify_video/{mediaId}/pl/mp4a/{bitrate}/{hash}.m3u8
```

一个 master 可以同时声明多个视频分辨率和多个音频 group。不能简单地为所有视频都选择全局最高码率音频；每条 `#EXT-X-STREAM-INF` 的 `AUDIO="group-id"` 必须与同 group 的 `#EXT-X-MEDIA:TYPE=AUDIO` 配对。helper 保存的每个 variant 已包含其精确音轨：

```json
{
  "id": "1280x720-1800000-a1b2c3d4",
  "width": 1280,
  "height": 720,
  "bandwidth": 1800000,
  "url": "https://video.twimg.com/.../avc1/1280x720/...m3u8",
  "audio": {
    "groupId": "audio-128000",
    "bitrate": 128000,
    "url": "https://video.twimg.com/.../mp4a/128000/...m3u8"
  }
}
```

master 及其所有子 playlist URL 都必须是 `https://video.twimg.com/*.m3u8`。相对 URI 使用 master URL 解析后仍执行同样校验，重定向也不能逃出该边界。

当前正式下载依赖 master 中的显式关联。如果某次只观察到独立视频和音频 media playlist、没有 master，诊断报告仍会记录它们，但不会猜测配对关系或自动下载。

## 6. 一帖多视频和网页托盘

帖子是媒体组，`mediaId` 是独立下载单元：

```text
Post 123456789
├── mediaId A，mediaIndex 1
├── mediaId B，mediaIndex 2
└── mediaId C，mediaIndex 3
```

content script 的关联顺序：

1. 从 `<video poster>` 或媒体封面 URL 的 `amplify_video_thumb/{mediaId}`、`ext_tw_video_thumb/{mediaId}` 提取 mediaId。
2. 向上找到最近的 `article`。
3. 从包含 `<time>` 的 `/status/{postId}` 链接取得帖子 URL、作者和发布时间。
4. 按 article 内不同 mediaId 的 DOM 顺序生成 `mediaIndex`。

每个已发现的 mediaId 在网页右下角显示一张卡片，包含缩略图、作者、媒体 ID、可选分辨率、关联音频码率和下载状态。插件不会自动下载。轮播中尚未播放的视频可能尚未请求 master，因此用户需要切换或播放每个视频。

候选展示采用两层隔离：首先只使用当前标签页内容脚本亲自捕获的候选，不把 helper 的全局内存候选灌入其他标签页；其次按当前 X 路由的 `pageUrl` 过滤，SPA 跳转后旧路由候选立即隐藏。当前路由内按 postId 分组，托盘默认量出五张视频卡片的高度，更多内容在托盘内部滚动。这样帖子详情页只显示该详情页媒体，信息流页面即使有较多视频也不会混成无边界长列表。

显示支持两种可持久化模式：

- `列表`：使用右下角托盘，适合集中查看当前页面的全部候选、任务与错误。
- `帖内`：保留一个小型模式切换条，并在每个帖子的收藏、分享操作栏右侧追加一个下载按钮。单视频帖子直接提交 helper 排在第一位的最高画质；多视频帖子先打开缩略图多选弹层，再为选中项目分别提交最高画质任务。

帖内控件先通过 mediaId 找到对应媒体所在的 `article`，再定位同时包含 `data-testid="reply"`、`like`、`bookmark` 或分享按钮的 `role="group"`，最后作为该操作组的最右侧子元素插入。按钮使用独立 Shadow DOM，并采用与 X 操作图标接近的尺寸、颜色和悬停效果。多选弹层使用挂载在页面根节点的独立 fixed 悬浮层，根据按钮的视口坐标定位和避让边缘，不受帖子容器的 `overflow` 裁剪或层叠上下文影响。MutationObserver 会在 X 动态重建操作栏后重新挂载按钮。

同一帖子只显示一个帖内下载按钮。单视频帖子点击后下载该视频最高画质；一帖多视频时，按钮带数量角标，点击后在按钮右下方弹出多选层。弹层列出缩略图、帖内序号、最高画质和当前任务状态，默认全选当前可下载或可重试的视频；下载中、已完成和不可用项保持可见但不可勾选。按钮会显示下载中旋转状态、完成勾选和失败重试状态；详细画质选择与逐任务取消仍保留在列表模式。

若 helper 未配置、代理不可用或 master 解析失败，托盘保留一个不可下载的待处理卡片并显示错误，而不是静默丢失发现结果。

## 7. helper API

除健康检查外，所有接口都要求 `Authorization: Bearer {token}`。

```text
GET    /v1/health

POST   /v1/capture-sessions
POST   /v1/capture-sessions/{id}/observations
GET    /v1/capture-sessions/{id}
POST   /v1/capture-sessions/{id}/finish

POST   /v1/candidates
GET    /v1/candidates
GET    /v1/candidates/{id}

POST   /v1/jobs
GET    /v1/jobs
GET    /v1/jobs/{id}
DELETE /v1/jobs/{id}
```

候选注册只提交 master 和页面上下文：

```json
{
  "masterUrl": "https://video.twimg.com/amplify_video/123/pl/master.m3u8",
  "context": {
    "postUrl": "https://x.com/someone/status/456",
    "postId": "456",
    "author": "someone",
    "mediaIndex": 1,
    "thumbnailUrl": "https://pbs.twimg.com/amplify_video_thumb/123/...jpg"
  }
}
```

创建任务只允许引用 helper 已知的候选与 variant：

```json
{
  "candidateId": "media-123",
  "variantId": "1280x720-1800000-a1b2c3d4"
}
```

客户端不能提交任意媒体 URL、输出目录、文件名或 FFmpeg 参数。

## 8. 下载队列和 FFmpeg

并发数由本地配置控制，允许 1–4，默认 1。相同 candidate/variant 的活动任务会去重；成功文件仍存在时重复提交直接返回已完成任务。失败、取消，或成功文件已被删除后可以重新创建任务。

独立视频和音频通过参数数组启动，不经过 shell：

```text
ffmpeg
-hide_banner -nostdin -loglevel warning
-progress pipe:1 -nostats
-i VIDEO_M3U8
-i AUDIO_M3U8
-map 0:v:0 -map 1:a:0
-c copy -movflags +faststart
-y TEMP_FILE.part.mp4
```

`-progress pipe:1` 输出的 `out_time_us` 和 `speed` 用于状态更新。取消时先终止整个进程组，两秒后仍未退出则强制结束。成功后临时文件在同一下载目录树内原子移动到最终路径；失败和取消会清理临时文件。

## 9. 配置与命名

默认配置：

```json
{
  "listenAddress": "127.0.0.1:17890",
  "downloadDir": "~/Downloads/X-Media",
  "tempDir": "~/Downloads/X-Media/.partial",
  "diagnosticsDir": "~/Library/Application Support/x-downloader/diagnostics",
  "tokenFile": "~/Library/Application Support/x-downloader/token",
  "ffmpegPath": "ffmpeg",
  "concurrency": 1,
  "filenameTemplate": "{date}_{author}_{postId}_{mediaIndex}_{mediaId}_{height}p.{ext}"
}
```

正常文件名示例：

```text
2026-07-13_someone_123456789_01_2076268346560196608_1080p.mp4
```

可用占位符为 `{date}`、`{author}`、`{postId}`、`{mediaIndex}`、`{mediaId}`、`{width}`、`{height}` 和 `{ext}`。helper 会清理不安全字符并限制文件名长度。模板不能包含 `/` 或 `\\`；下载目录只能通过 helper 本地配置修改。

## 10. 代理行为

helper 使用 Go 标准 HTTP transport 获取 master，因此会读取启动进程环境中的 `HTTP_PROXY`、`HTTPS_PROXY` 和 `NO_PROXY`。FFmpeg 子进程继承 helper 的环境；为了兼容 FFmpeg，启动时同时设置小写 `http_proxy`、`https_proxy` 更稳妥。

代理只用于 helper 到 `video.twimg.com` 的出站请求。服务本身始终监听 `127.0.0.1`，浏览器访问本地 API 应通过 `NO_PROXY=127.0.0.1,localhost` 绕过代理。

## 11. 安全与当前限制

- helper 强制绑定 `127.0.0.1`。
- 使用高熵 bearer token，token 不放在 URL 中。
- 网络获取限定为 Twitter 视频 CDN 的 HTTPS m3u8。
- 请求体限制为 64 KiB；master 最大 2 MiB；下载并发最多 4。
- 默认不读取或传递浏览器 Cookie、Authorization 等请求头。
- DOM 元数据不受信任，helper 会校验帖子、缩略图、mediaId 与路径。
- 候选和任务状态当前仅保存在 helper 内存中；重启后需要重新播放视频以注册候选，已生成的 MP4 不受影响。
- 当前仅覆盖含独立 H.264/AVC 视频和 AAC 音频且由 master 明确关联的样本；其他编码或单文件 MP4 需要后续样本驱动扩展。
- 批量勾选下载、配置 UI、开机启动和状态持久化尚未实现。

## 12. 已完成验证

1. 最高画质 master 重写与 JavaScript 单元测试。
2. `webRequest` 诊断捕获、playlist 分类与本地报告。
3. 真实 X 样本中发现 master、多个视频 variant 和多个独立音频 group。
4. helper master 解析、精确音轨配对和严格 URL 校验。
5. 页面帖子上下文、一帖多视频卡片和用户画质选择。
6. 下载队列、取消、命名、FFmpeg 进度与原子落盘。
7. 候选注册到下载完成的 helper API 自动化流程测试。
