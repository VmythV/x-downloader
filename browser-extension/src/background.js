'use strict';

const CAPTURE_STORAGE_KEY = 'hlsMasterCaptures';
const DIAGNOSTIC_STATE_KEY = 'diagnosticState';
const HELPER_SETTINGS_KEY = 'helperSettings';
const FINISH_ALARM_NAME = 'x-downloader-diagnostic-finish';
const MAX_CAPTURE_COUNT = 100;
const MAX_PENDING_OBSERVATIONS = 20;

let diagnosticState = null;
let flushTimer = null;
let flushInFlight = null;
const pendingObservations = new Map();

const stateReady = chrome.storage.session.get(DIAGNOSTIC_STATE_KEY).then((stored) => {
  diagnosticState = stored[DIAGNOSTIC_STATE_KEY] || null;
});

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
    throw new Error('helper 地址必须是 http://127.0.0.1:端口');
  }
  if (url.pathname !== '/' || url.search || url.hash) {
    throw new Error('helper 地址不能包含路径、查询参数或片段');
  }
  return url.origin;
}

function sanitizeCapture(capture) {
  const masterUrl = new URL(capture.masterUrl);
  if (masterUrl.protocol !== 'https:' || masterUrl.hostname !== 'video.twimg.com') {
    throw new Error('Unsupported master playlist URL');
  }
  const mediaMatch = /\/(?:amplify_video|ext_tw_video)\/(\d+)(?:\/|$)/.exec(masterUrl.pathname);

  return {
    capturedAt: Number(capture.capturedAt) || Date.now(),
    masterUrl: masterUrl.href,
    mediaId: mediaMatch?.[1] || '',
    selected: capture.selected || null,
    variants: Array.isArray(capture.variants) ? capture.variants.slice(0, 20) : [],
    renditions: Array.isArray(capture.renditions) ? capture.renditions.slice(0, 20) : [],
  };
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

function sanitizeObservation(details) {
  const url = new URL(details.url);
  if (url.protocol !== 'https:'
    || url.hostname !== 'video.twimg.com'
    || !url.pathname.toLowerCase().endsWith('.m3u8')) {
    throw new Error('Unsupported observation URL');
  }

  return {
    url: url.href,
    seenAt: new Date(details.timeStamp || Date.now()).toISOString(),
    pageUrl: typeof details.initiator === 'string' ? details.initiator.slice(0, 2048) : '',
    requestType: typeof details.type === 'string' ? details.type.slice(0, 32) : '',
  };
}

async function saveCapture(capture) {
  const stored = await chrome.storage.session.get(CAPTURE_STORAGE_KEY);
  const captures = Array.isArray(stored[CAPTURE_STORAGE_KEY])
    ? stored[CAPTURE_STORAGE_KEY]
    : [];

  const identity = `${capture.mediaId}|${capture.masterUrl}`;
  const withoutPrevious = captures.filter((item) => (
    `${item.mediaId}|${item.masterUrl}` !== identity
  ));
  withoutPrevious.unshift(capture);

  await chrome.storage.session.set({
    [CAPTURE_STORAGE_KEY]: withoutPrevious.slice(0, MAX_CAPTURE_COUNT),
  });
}

async function getHelperSettings() {
  const stored = await chrome.storage.local.get(HELPER_SETTINGS_KEY);
  const settings = stored[HELPER_SETTINGS_KEY];
  if (!settings) {
    throw new Error('请先配置 helper 地址和配对令牌');
  }
  return normalizeHelperSettings(settings);
}

function normalizeHelperSettings(settings) {
  const baseUrl = normalizeHelperBaseUrl(settings?.baseUrl);
  const token = String(settings?.token || '').trim();
  if (token.length < 32) {
    throw new Error('配对令牌无效');
  }
  return { baseUrl, token };
}

async function helperRequestWithSettings(settings, path, options = {}) {
  const response = await fetch(`${settings.baseUrl}${path}`, {
    ...options,
    headers: {
      Authorization: `Bearer ${settings.token}`,
      ...(options.body ? { 'Content-Type': 'application/json' } : {}),
      ...options.headers,
    },
  });

  let payload = null;
  try {
    payload = await response.json();
  } catch {
    // The error below includes the status when the body is not JSON.
  }

  if (!response.ok) {
    if (response.status === 401) {
      throw new Error('配对令牌无效');
    }
    throw new Error(payload?.error || `helper 请求失败：HTTP ${response.status}`);
  }
  return payload;
}

async function helperRequest(path, options = {}) {
  const settings = await getHelperSettings();
  return helperRequestWithSettings(settings, path, options);
}

async function persistDiagnosticState() {
  await chrome.storage.session.set({ [DIAGNOSTIC_STATE_KEY]: diagnosticState });
}

async function queueObservation(details) {
  await stateReady;
  if (!diagnosticState || diagnosticState.status !== 'active') {
    return;
  }
  if (Number.isInteger(details.tabId) && details.tabId >= 0 && details.tabId !== diagnosticState.tabId) {
    return;
  }

  let observation;
  try {
    observation = sanitizeObservation(details);
  } catch {
    return;
  }

  pendingObservations.set(observation.url, observation);
  if (pendingObservations.size >= MAX_PENDING_OBSERVATIONS) {
    await flushObservations();
    return;
  }
  if (!flushTimer) {
    flushTimer = setTimeout(() => {
      flushTimer = null;
      flushObservations().catch(() => {});
    }, 500);
  }
}

async function flushObservations() {
  await stateReady;
  if (flushInFlight) {
    return flushInFlight;
  }
  if (!diagnosticState || diagnosticState.status !== 'active' || pendingObservations.size === 0) {
    return null;
  }

  const batch = [...pendingObservations.values()];
  pendingObservations.clear();
  const sessionId = diagnosticState.sessionId;

  flushInFlight = helperRequest(`/v1/capture-sessions/${sessionId}/observations`, {
    method: 'POST',
    body: JSON.stringify({ observations: batch }),
  }).then(async (session) => {
    if (diagnosticState?.sessionId === sessionId) {
      diagnosticState.observationCount = session.observations?.length || 0;
      diagnosticState.uniquePlaylistCount = session.probes?.length || 0;
      diagnosticState.lastError = '';
      await persistDiagnosticState();
    }
    return session;
  }).catch(async (error) => {
    for (const observation of batch) {
      pendingObservations.set(observation.url, observation);
    }
    if (diagnosticState?.sessionId === sessionId) {
      diagnosticState.lastError = error.message;
      await persistDiagnosticState();
    }
    throw error;
  }).finally(() => {
    flushInFlight = null;
  });

  return flushInFlight;
}

async function startDiagnostic(message) {
  await stateReady;
  if (diagnosticState?.status === 'active') {
    throw new Error('已经有一个诊断会话正在运行');
  }

  const durationSeconds = Math.min(120, Math.max(5, Number(message.durationSeconds) || 15));
  const tabId = Number(message.tabId);
  if (!Number.isInteger(tabId) || tabId < 0) {
    throw new Error('找不到当前 X 标签页');
  }
  const tab = await chrome.tabs.get(tabId);
  if (!isAllowedPage(tab.url)) {
    throw new Error('请在 X/Twitter 页面中启动诊断');
  }

  pendingObservations.clear();
  const session = await helperRequest('/v1/capture-sessions', { method: 'POST' });
  const now = Date.now();
  diagnosticState = {
    status: 'active',
    sessionId: session.id,
    tabId,
    startedAt: now,
    endsAt: now + durationSeconds * 1000,
    observationCount: 0,
    uniquePlaylistCount: 0,
    lastError: '',
    report: null,
  };
  await persistDiagnosticState();
  await chrome.alarms.create(FINISH_ALARM_NAME, { when: diagnosticState.endsAt });
  return diagnosticState;
}

async function finishDiagnostic() {
  await stateReady;
  if (!diagnosticState || diagnosticState.status !== 'active') {
    return diagnosticState;
  }

  if (flushTimer) {
    clearTimeout(flushTimer);
    flushTimer = null;
  }
  await flushObservations();

  const sessionId = diagnosticState.sessionId;
  const report = await helperRequest(`/v1/capture-sessions/${sessionId}/finish`, { method: 'POST' });
  diagnosticState = {
    ...diagnosticState,
    status: 'finished',
    finishedAt: Date.now(),
    report,
    lastError: '',
  };
  await chrome.alarms.clear(FINISH_ALARM_NAME);
  await persistDiagnosticState();
  return diagnosticState;
}

async function saveHelperSettings(message) {
  const settings = normalizeHelperSettings(message);
  await chrome.storage.local.set({ [HELPER_SETTINGS_KEY]: settings });
  return settings;
}

async function testAndSaveHelperSettings(message) {
  const settings = normalizeHelperSettings(message);
  const health = await helperRequestWithSettings(settings, '/v1/health', { cache: 'no-store' });

  // Health is intentionally public. Probe a read-only protected endpoint too so
  // a successful test proves that the pairing token is accepted.
  await helperRequestWithSettings(settings, '/v1/candidates', { cache: 'no-store' });
  await chrome.storage.local.set({ [HELPER_SETTINGS_KEY]: settings });
  return health;
}

async function registerCandidate(masterUrl, context) {
  return helperRequest('/v1/candidates', {
    method: 'POST',
    body: JSON.stringify({
      masterUrl,
      context: sanitizePageContext(context),
    }),
  });
}

chrome.webRequest.onBeforeRequest.addListener((details) => {
  queueObservation(details).catch(() => {});
}, {
  urls: ['https://video.twimg.com/*'],
  types: ['xmlhttprequest', 'media', 'other'],
});

chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === FINISH_ALARM_NAME) {
    finishDiagnostic().catch(async (error) => {
      if (diagnosticState) {
        diagnosticState.lastError = error.message;
        await persistDiagnosticState();
      }
    });
  }
});

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
          await saveCapture(capture);
          await queueObservation({
            url: capture.masterUrl,
            timeStamp: capture.capturedAt,
            tabId: sender.tab.id,
            initiator: sender.tab.url,
            type: 'xmlhttprequest',
          });
          try {
            const candidate = await registerCandidate(capture.masterUrl, message.context);
            if (sender.tab?.id != null) {
              chrome.tabs.sendMessage(sender.tab.id, {
                type: 'candidate-updated',
                candidate,
              }).catch(() => {});
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
    case 'diagnostic-start':
      operation = startDiagnostic(message);
      break;
    case 'diagnostic-finish':
      operation = finishDiagnostic();
      break;
    case 'diagnostic-status':
      operation = stateReady.then(() => diagnosticState);
      break;
    default:
      return false;
  }

  operation
    .then((result) => sendResponse({ ok: true, result }))
    .catch((error) => sendResponse({ ok: false, error: error.message }));
  return true;
});
