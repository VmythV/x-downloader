# HLS 捕获诊断操作指南

诊断的目标是记录 X 播放器真实请求的 m3u8，并回答以下问题：

- 是否存在 master playlist。
- 同一 mediaId 下有哪些视频清晰度和音频码率。
- helper 能否在不使用 Cookie 的情况下获取 playlist。

诊断不会下载视频分片，也不会生成 MP4。

## 1. 构建并启动 helper

```bash
cd helper
go build -o x-downloader-helper ./cmd/x-downloader-helper
./x-downloader-helper
```

默认监听：

```text
http://127.0.0.1:17890
```

查看插件配对令牌：

```bash
./x-downloader-helper -print-token
```

令牌文件默认位于 macOS 用户配置目录：

```text
~/Library/Application Support/x-downloader/token
```

如需覆盖配置：

```bash
./x-downloader-helper -config ./config.json
```

配置示例见 `helper/config.example.json`。

如果本机直连无法访问 `video.twimg.com`，helper 也需要带代理环境启动。代理配置方式见 [下载功能实测指南](download-guide.md#x-需要代理时)；诊断抓取和正式下载使用同一套环境变量。

## 2. 加载插件

1. 打开 `chrome://extensions`。
2. 开启开发者模式。
3. 选择“加载已解压的扩展程序”。
4. 选择仓库中的 `browser-extension` 目录。
5. 修改代码后，需要在扩展页面点击重新加载，并刷新已打开的 X 页面。

## 3. 执行诊断

1. 打开 X/Twitter 页面。
2. 找到需要测试的视频，保持当前标签页激活。
3. 点击浏览器工具栏中的 X Downloader 图标。
4. helper 地址保持 `http://127.0.0.1:17890`。
5. 粘贴 `-print-token` 输出的令牌。
6. 点击“保存并测试”；扩展会同时校验配对令牌并持久化设置。
7. 设置诊断时长，默认 15 秒。
8. 点击“开始诊断”。
9. 在倒计时期间播放视频；多视频帖子需要切换并播放每一个视频。
10. 等待自动结束，或点击“提前结束”。

弹窗会显示：

- 捕获请求数量。
- 唯一 playlist 数量。
- 是否发现 master。
- mediaId 数量。
- 探测失败数量。

## 4. 查看本地报告

报告默认写入：

```text
~/Library/Application Support/x-downloader/diagnostics/{sessionId}/
├── requests.jsonl
├── playlists/
│   └── {url-sha256}.m3u8
└── report.json
```

`report.json` 中：

- `masterDetected` 表示是否发现 master。
- `observationCount` 是观察总次数。
- `uniquePlaylistCount` 是去重后的 URL 数量。
- `failedProbeCount` 是 helper 无法抓取的 playlist 数量。
- `media` 按 mediaId 汇总 master、视频和音频。

## 5. 建议测试样本

至少执行三次独立诊断：

1. 单视频帖子，只播放一次。
2. 一条帖子含多个视频，逐个切换播放。
3. 首页信息流中多个视频自动播放。

如果第一次没有发现 master，刷新 X 页面后立即开始一次较长的诊断，或者先开始诊断再触发视频播放。

## 6. 安全说明

- 诊断只接受 `https://video.twimg.com/*.m3u8`。
- helper 拒绝非回环监听地址和非 Twitter CDN URL。
- helper 不读取浏览器 Cookie。
- playlist URL 和内容可能包含短期访问凭据，诊断目录按敏感数据处理。
- 不需要分享原始诊断目录；审核时优先分享去除 URL 查询参数后的摘要。
