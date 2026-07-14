# X Downloader 用户使用说明

## 1. 准备环境

需要：

- Go 1.24 或更高版本。
- Chromium、Chrome 或兼容浏览器。
- FFmpeg。

检查 FFmpeg：

```bash
ffmpeg -version
```

macOS 可以使用 Homebrew 安装：

```bash
brew install ffmpeg
```

## 2. 构建并启动 Helper

```bash
cd helper
go test ./...
go build -o x-downloader-helper ./cmd/x-downloader-helper
./x-downloader-helper
```

Helper 默认监听 `http://127.0.0.1:17890`。终端出现 `starting helper` 后保持该进程运行。

### 自定义 Helper 配置

复制配置示例：

```bash
cd helper
cp config.example.json config.json
```

编辑 `config.json` 后启动：

```bash
./x-downloader-helper -config ./config.json
```

主要配置：

- `downloadDir`：首次启动或恢复默认值时使用的 MP4 目录。
- `tempDir`：下载中的临时文件目录。
- `stateDir`：候选和任务历史目录。
- `ffmpegPath`：FFmpeg 命令或绝对路径。
- `concurrency`：同时下载数量，允许 1–4。
- `retryCount`：任务失败后自动重试次数，允许 0–5。
- `filenameTemplate`：文件名模板。

## 3. 加载浏览器扩展

1. 打开 `chrome://extensions`。
2. 开启“开发者模式”。
3. 点击“加载已解压的扩展程序”。
4. 选择仓库中的 `browser-extension` 目录。
5. 修改或更新扩展代码后，需要在这里点击“重新加载”，然后刷新已经打开的 X 页面。

## 4. 配对 Helper

读取令牌：

```bash
cd helper
./x-downloader-helper -print-token
```

如果 Helper 使用自定义配置且修改了 `tokenFile`，打印令牌时也要传同一个配置：

```bash
./x-downloader-helper -config ./config.json -print-token
```

打开扩展弹窗：

1. Helper 地址保持 `http://127.0.0.1:17890`。
2. 粘贴配对令牌。
3. 点击“保存并检查”。
4. 确认顶部显示“可以下载”。

弹窗会分别检查 API 版本、FFmpeg、下载目录和状态持久化。显示“需要处理”时，按检查项给出的原因修复。

## 5. 设置下载目录

点击扩展弹窗右上角的齿轮按钮进入设置页：

1. 在“下载目录”中点击“选择文件夹…”。
2. 使用系统文件夹选择器选定位置。
3. 点击“保存目录”。
4. 页面显示“当前目录已保存”后，新创建的任务会使用该目录。

目录保存在 Helper 的 `stateDir/settings.json`，关闭浏览器或重启 Helper 后仍会使用相同位置。修改目录不会搬移已完成文件，也不会改变已经排队或下载中的任务。

也可以手动输入绝对路径。Linux 的原生选择器需要 `zenity` 或 `kdialog`；未安装时仍可手动填写。点击“恢复配置默认目录”会恢复 Helper 启动配置中的 `downloadDir`。

### 设置文件命名、并发和失败重试

“下载规则”区域支持：

- 文件命名模板：可组合 `{date}`、`{author}`、`{postId}`、`{mediaIndex}`、`{mediaId}`、`{width}`、`{height}` 和 `{ext}`。
- 同时下载：允许 1–4 个任务。提高后会立即放行等待任务；降低不会中断已经开始的任务。
- 失败自动重试：允许 0–5 次，只重试 FFmpeg 或网络执行失败；用户主动取消不会重试。

命名模板和重试次数应用于保存后新建的任务。点击“恢复配置默认值”可以恢复 Helper 启动配置。

### 下载总开关和通知

- 关闭“启用视频下载”后，扩展停止识别新视频并隐藏 X 页面内的下载入口，但不会取消已经进行中的任务。
- “下载结果通知”控制完成和最终失败通知。

## 6. 下载视频

1. 打开 X/Twitter 页面。
2. 播放要下载的视频，等待扩展捕获 HLS master。
3. 页面右下角出现 X Downloader 控件。

### 列表模式

- 每个已识别视频显示为一张卡片。
- 可以选择分辨率并点击“下载”。
- 下载中可以取消，失败后可以重试。
- 托盘只展示当前标签页、当前 X 路由捕获的视频。

### 帖内模式

- 下载图标位于帖子回复、收藏和分享操作栏右侧。
- 单视频帖子点击后直接下载最高画质。
- 多视频帖子点击后打开独立悬浮框，可以查看缩略图并多选下载。
- 下载中、已完成和暂不可用的视频会显示状态，但不会重复勾选。

### 多视频帖子

X 通常只请求当前正在播放的视频。轮播中有多个视频时，请逐个切换并播放；托盘提示中的“已检测数量”会随捕获结果增加。

## 7. 查看任务和文件

点击浏览器工具栏中的 X Downloader：

- “最近任务”显示等待、下载、完成、失败和取消状态。
- 已完成任务可以点击“显示文件”，在 Finder 或资源管理器中定位。
- 失败或取消任务可以点击“重新下载”。
- 默认开启下载完成/失败通知，可以在弹窗或设置页关闭。

Helper 会保留最近 300 个候选和 500 个任务。扩展页面内最多保留 250 个候选和 500 个任务，避免长时间浏览信息流后持续占用内存。

## 8. Helper 重启

候选和任务状态保存在 `stateDir`。Helper 重启后：

- 已完成历史仍可查看和定位。
- 重启时仍在等待或下载的任务会标记为“被 Helper 重启中断”，可以重新下载。
- 已经生成的 MP4 不会被删除。
- 设置页保存的下载目录会自动恢复。

## 9. 代理环境

如果本机需要代理才能访问 `video.twimg.com`，在启动 Helper 的同一个终端设置代理。以 HTTP 代理 `127.0.0.1:7890` 为例：

```bash
export http_proxy=http://127.0.0.1:7890
export https_proxy=http://127.0.0.1:7890
export HTTP_PROXY=http://127.0.0.1:7890
export HTTPS_PROXY=http://127.0.0.1:7890
export NO_PROXY=127.0.0.1,localhost
export no_proxy=127.0.0.1,localhost
./x-downloader-helper -config ./config.json
```

FFmpeg 会继承 Helper 的环境变量。本地 API 必须通过 `NO_PROXY` 绕过代理。

## 10. 常见问题

### 弹窗显示“Helper 不可用”

- 确认 Helper 终端仍在运行。
- 确认端口与配置一致。
- 重新打印并保存配对令牌。
- 如果刚更新代码，重新构建并重启 Helper。

### 弹窗显示“FFmpeg 不可用”

- 执行 `ffmpeg -version`。
- 或把 `config.json` 的 `ffmpegPath` 设置为 FFmpeg 绝对路径。
- 重启 Helper 后再次刷新状态。

### 视频没有下载按钮

- 主动播放视频，等待 master 请求。
- 多视频轮播需要逐个切换播放。
- 确认扩展是在 X 页面打开前加载；重新加载扩展后要刷新 X 页面。
- 切换到列表模式查看候选错误；“重试连接”可以重新向 Helper 注册。

### Helper 已连接但下载失败

- 若错误包含 `video.twimg.com` 超时或连接失败，检查代理。
- 若提示候选过期，重新播放视频。
- 若提示 Helper 重启中断，点击“重新下载”。
- 如果设置了自动重试，任务会显示当前尝试次数；达到上限后才标记为最终失败。

### 下载目录无法保存

- 目录必须是本机绝对路径，并且当前用户需要有写入权限。
- macOS 和 Windows 会使用系统文件夹选择器。
- Linux 请安装 `zenity` 或 `kdialog`，或者直接手动输入绝对路径。
- 外置磁盘暂时不可用时，Helper 仍可启动，但会提示下载目录不可写；重新连接磁盘或选择新目录即可。

### 清空本地状态

先退出 Helper，再删除配置中的 `stateDir`。这只清理候选和任务历史，不删除已经完成的 MP4。

## 11. 安全提示

- 只下载你拥有或获准保存的公开内容。
- 配对令牌等同于本机 Helper 的访问凭据，不要公开分享。
- 项目不读取浏览器 Cookie，也不会把媒体或使用记录上传到外部服务。
- 详细数据范围见 [隐私说明](privacy.md)。
