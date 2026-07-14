'use strict';

const HELPER_SETTINGS_KEY = 'helperSettings';
const EXPECTED_API_VERSION = '2';
const HELPER_REQUEST_TIMEOUT_MS = 20_000;
const DIRECTORY_PICKER_TIMEOUT_MS = 5 * 60_000;

function isAllowedPage(urlValue) {
  try {
    const url = new URL(urlValue);
    return url.protocol === 'https:'
      && ['x.com', 'mobile.x.com', 'pro.x.com', 'twitter.com', 'mobile.twitter.com', 'pro.twitter.com']
        .includes(url.hostname);
  } catch {
    return false;
  }
}

function normalizeHelperBaseUrl(value) {
  const url = new URL(value);
  if (url.protocol !== 'http:' || url.hostname !== '127.0.0.1' || url.username || url.password) {
    throw new Error('Helper 地址必须是 http://127.0.0.1:端口');
  }
  if (url.pathname !== '/' || url.search || url.hash) {
    throw new Error('Helper 地址不能包含路径、查询参数或片段');
  }
  return url.origin;
}

function normalizeHelperSettings(settings) {
  const baseUrl = normalizeHelperBaseUrl(settings?.baseUrl);
  const token = String(settings?.token || '').trim();
  if (token.length < 32) {
    throw new Error('配对令牌无效');
  }
  return { baseUrl, token };
}

function sanitizeCapture(capture) {
  const masterUrl = new URL(capture?.masterUrl);
  if (masterUrl.protocol !== 'https:' || masterUrl.hostname !== 'video.twimg.com') {
    throw new Error('不支持的播放列表地址');
  }
  const mediaMatch = /\/(?:amplify_video|ext_tw_video)\/(\d+)(?:\/|$)/.exec(masterUrl.pathname);
  if (!mediaMatch) {
    throw new Error('播放列表中没有媒体 ID');
  }
  return { masterUrl: masterUrl.href, mediaId: mediaMatch[1] };
}

function sanitizePageContext(context) {
  if (!context || typeof context !== 'object') {
    return {};
  }
  const sanitized = {
    pageUrl: String(context.pageUrl || '').slice(0, 2048),
    postUrl: String(context.postUrl || '').slice(0, 2048),
    postId: /^\d+$/.test(context.postId || '') ? context.postId : '',
    author: String(context.author || '').slice(0, 64),
    mediaIndex: Math.min(20, Math.max(0, Number(context.mediaIndex) || 0)),
    thumbnailUrl: String(context.thumbnailUrl || '').slice(0, 2048),
  };
  if (context.createdAt) {
    sanitized.createdAt = context.createdAt;
  }
  return sanitized;
}

async function getHelperSettings() {
  const stored = await chrome.storage.local.get(HELPER_SETTINGS_KEY);
  const settings = stored[HELPER_SETTINGS_KEY];
  if (!settings) {
    throw new Error('请先配置 Helper 地址和配对令牌');
  }
  return normalizeHelperSettings(settings);
}

function localizedHelperError(error, responseStatus = 0) {
  if (error?.name === 'AbortError') {
    return new Error('Helper 响应超时，请检查进程和代理');
  }
  const message = error?.message || String(error || '');
  if (responseStatus === 401 || /invalid bearer token/i.test(message)) {
    return new Error('配对令牌无效');
  }
  if (/failed to fetch|networkerror|load failed/i.test(message)) {
    return new Error('无法连接 Helper，请确认它正在运行');
  }
  if (/ffmpeg/i.test(message) && /not found|executable file|start/i.test(message)) {
    return new Error('FFmpeg 不可用，请安装 FFmpeg 或检查配置路径');
  }
  if (/download interrupted because helper restarted/i.test(message)) {
    return new Error('Helper 重启中断了下载，可以重新下载');
  }
  if (/download queue is full/i.test(message)) {
    return new Error('下载队列已满，请稍后重试');
  }
  if (/media candidate not found/i.test(message)) {
    return new Error('下载候选已过期，请重新播放该视频');
  }
  if (/download directory must be absolute/i.test(message)) {
    return new Error('下载目录必须是绝对路径');
  }
  if (/download directory must not be empty/i.test(message)) {
    return new Error('下载目录不能为空');
  }
  if (/download directory is not writable|create download directory/i.test(message)) {
    return new Error('下载目录不可写，请选择其他文件夹');
  }
  if (/directory picker requires zenity or kdialog/i.test(message)) {
    return new Error('Linux 文件夹选择器需要安装 zenity 或 kdialog，也可以手动输入绝对路径');
  }
  if (/open .* directory picker|native directory picker is not supported/i.test(message)) {
    return new Error('无法打开系统文件夹选择器，可以手动输入绝对路径');
  }
  return new Error(message || `Helper 请求失败${responseStatus ? `：HTTP ${responseStatus}` : ''}`);
}

async function helperRequestWithSettings(settings, path, options = {}) {
  const { timeoutMs = HELPER_REQUEST_TIMEOUT_MS, ...fetchOptions } = options;
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), timeoutMs);
  let response;
  try {
    response = await fetch(`${settings.baseUrl}${path}`, {
      ...fetchOptions,
      signal: controller.signal,
      headers: {
        Authorization: `Bearer ${settings.token}`,
        ...(fetchOptions.body ? { 'Content-Type': 'application/json' } : {}),
        ...fetchOptions.headers,
      },
    });
  } catch (error) {
    throw localizedHelperError(error);
  } finally {
    clearTimeout(timeout);
  }

  let payload = null;
  try {
    payload = await response.json();
  } catch {
    // The status-based error below covers non-JSON responses.
  }
  if (!response.ok) {
    throw localizedHelperError(new Error(payload?.error || ''), response.status);
  }
  return payload;
}

async function helperRequest(path, options = {}) {
  return helperRequestWithSettings(await getHelperSettings(), path, options);
}

function assertCompatibleStatus(status) {
  if (String(status?.apiVersion || '') !== EXPECTED_API_VERSION) {
    throw new Error(`Helper API 版本不兼容，需要 ${EXPECTED_API_VERSION}，当前为 ${status?.apiVersion || '未知'}`);
  }
  return status;
}

async function saveHelperSettings(message) {
  const settings = normalizeHelperSettings(message);
  await chrome.storage.local.set({ [HELPER_SETTINGS_KEY]: settings });
  return settings;
}

async function testAndSaveHelperSettings(message) {
  const settings = normalizeHelperSettings(message);
  const status = assertCompatibleStatus(await helperRequestWithSettings(settings, '/v1/status', { cache: 'no-store' }));
  await chrome.storage.local.set({ [HELPER_SETTINGS_KEY]: settings });
  return status;
}

async function registerCandidate(masterUrl, context) {
  return helperRequest('/v1/candidates', {
    method: 'POST',
    body: JSON.stringify({ masterUrl, context: sanitizePageContext(context) }),
  });
}

async function showJobNotification(message) {
  const status = String(message.status || '');
  if (!['completed', 'failed'].includes(status)) {
    return null;
  }
  const jobId = String(message.jobId || '').slice(0, 128);
  const detail = String(message.detail || '').slice(0, 240);
  return chrome.notifications.create(`x-downloader-${jobId}-${status}`, {
    type: 'basic',
    iconUrl: 'icons/icon128.png',
    title: status === 'completed' ? '视频下载完成' : '视频下载失败',
    message: detail || (status === 'completed' ? '文件已保存到下载目录' : '打开扩展查看失败原因'),
  });
}

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  let operation;

  switch (message?.type) {
    case 'hls-master-captured':
      if (!isAllowedPage(sender.tab?.url)) {
        return false;
      }
      operation = Promise.resolve()
        .then(() => sanitizeCapture(message.capture))
        .then(async (capture) => {
          try {
            const candidate = await registerCandidate(capture.masterUrl, message.context);
            if (sender.tab?.id != null) {
              chrome.tabs.sendMessage(sender.tab.id, { type: 'candidate-updated', candidate }).catch(() => {});
            }
            return { candidate };
          } catch (error) {
            return { candidate: null, candidateError: error.message };
          }
        });
      break;
    case 'media-context-updated':
      if (!isAllowedPage(sender.tab?.url)) {
        return false;
      }
      operation = registerCandidate(message.masterUrl, message.context)
        .then((candidate) => ({ candidate }));
      break;
    case 'helper-settings-get':
      operation = chrome.storage.local.get(HELPER_SETTINGS_KEY)
        .then((stored) => stored[HELPER_SETTINGS_KEY] || {
          baseUrl: 'http://127.0.0.1:17890',
          token: '',
        });
      break;
    case 'helper-settings-save':
      operation = saveHelperSettings(message);
      break;
    case 'helper-settings-test-and-save':
      operation = testAndSaveHelperSettings(message);
      break;
    case 'helper-status':
      operation = helperRequest('/v1/status', { cache: 'no-store' }).then(assertCompatibleStatus);
      break;
    case 'app-settings-get':
      operation = helperRequest('/v1/settings', { cache: 'no-store' });
      break;
    case 'app-settings-update':
      operation = helperRequest('/v1/settings', {
        method: 'PUT',
        body: JSON.stringify({ downloadDir: String(message.downloadDir || '') }),
      });
      break;
    case 'app-settings-pick-download-directory':
      operation = helperRequest('/v1/settings/pick-download-directory', {
        method: 'POST',
        timeoutMs: DIRECTORY_PICKER_TIMEOUT_MS,
      });
      break;
    case 'candidate-list':
      operation = helperRequest('/v1/candidates');
      break;
    case 'job-create':
      operation = helperRequest('/v1/jobs', {
        method: 'POST',
        body: JSON.stringify({
          candidateId: message.candidateId,
          variantId: message.variantId || '',
        }),
      });
      break;
    case 'job-list':
      operation = helperRequest('/v1/jobs');
      break;
    case 'job-get':
      operation = helperRequest(`/v1/jobs/${encodeURIComponent(message.jobId)}`);
      break;
    case 'job-cancel':
      operation = helperRequest(`/v1/jobs/${encodeURIComponent(message.jobId)}`, { method: 'DELETE' });
      break;
    case 'job-reveal':
      operation = helperRequest(`/v1/jobs/${encodeURIComponent(message.jobId)}/reveal`, { method: 'POST' });
      break;
    case 'job-notification':
      operation = showJobNotification(message);
      break;
    default:
      return false;
  }

  Promise.resolve(operation)
    .then((result) => sendResponse({ ok: true, result }))
    .catch((error) => sendResponse({ ok: false, error: localizedHelperError(error).message }));
  return true;
});
