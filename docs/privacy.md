# X Downloader 本地数据与隐私说明

X Downloader 采用本地优先设计，不提供云端服务，也不包含遥测或分析 SDK。

## 浏览器扩展处理的数据

- X/Twitter 当前页面 URL、帖子 ID、作者、发布时间和媒体缩略图 URL。
- 浏览器已经请求的 `video.twimg.com` HLS master URL。
- Helper 地址、配对令牌、显示模式和通知偏好。
- 当前标签页内的候选与任务状态。

扩展使用 `storage` 保存设置，使用 `notifications` 显示下载结果；只在 X/Twitter、`video.twimg.com` 和 `127.0.0.1` 范围内工作。

## Helper 保存的数据

- 高熵配对令牌。
- 最近最多 300 个媒体候选。
- 最近最多 500 个下载任务及其本地输出路径。
- 下载中的临时文件与完成的 MP4。

候选和任务默认位于用户配置目录的 `x-downloader/state/`，下载文件默认位于 `~/Downloads/X-Media/`。

## 不会处理或上传的数据

- 不读取或保存浏览器 Cookie。
- 不读取 X 的账号密码或 Authorization 请求头。
- 不绕过 DRM、付费或私密访问控制。
- 不把视频、URL、令牌或使用记录上传到项目作者或第三方服务。

Helper 只监听 `127.0.0.1`，除公开健康检查外的 API 都要求 bearer token；媒体抓取仅允许 `https://video.twimg.com/*.m3u8`。

## 清理数据

退出 Helper 后删除 `stateDir` 可以清除候选和任务历史；删除 `tokenFile` 会在下次启动时生成新令牌。删除浏览器扩展会清理其扩展存储。已经下载的 MP4 需要用户自行删除。
