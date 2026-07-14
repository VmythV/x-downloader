'use strict';

const elements = {
  badge: document.querySelector('#connection-badge'),
  openSettings: document.querySelector('#open-settings'),
  readinessTitle: document.querySelector('#readiness-title'),
  readinessSummary: document.querySelector('#readiness-summary'),
  readinessList: document.querySelector('#readiness-list'),
  refresh: document.querySelector('#refresh'),
  settings: document.querySelector('#connection-settings'),
  helperUrl: document.querySelector('#helper-url'),
  helperToken: document.querySelector('#helper-token'),
  testHelper: document.querySelector('#test-helper'),
  connectionStatus: document.querySelector('#connection-status'),
  notifications: document.querySelector('#download-notifications'),
  historySummary: document.querySelector('#history-summary'),
  jobList: document.querySelector('#job-list'),
  error: document.querySelector('#error'),
};

let latestJobs = [];

async function sendMessage(message) {
  const response = await chrome.runtime.sendMessage(message);
  if (!response?.ok) {
    throw new Error(response?.error || '扩展后台没有响应');
  }
  return response.result;
}

function showError(error) {
  elements.error.textContent = error?.message || String(error || '');
}

function addCheck(text, ok) {
  const item = document.createElement('li');
  if (!ok) item.className = 'problem';
  const mark = document.createElement('span');
  mark.className = 'mark';
  mark.textContent = ok ? '✓' : '!';
  const label = document.createElement('span');
  label.textContent = text;
  item.append(mark, label);
  elements.readinessList.appendChild(item);
}

function renderDisconnected(message) {
  elements.badge.className = 'badge offline';
  elements.badge.textContent = '未连接';
  elements.readinessTitle.textContent = 'Helper 不可用';
  elements.readinessSummary.textContent = message || '请启动 Helper 并检查连接设置';
  elements.readinessList.replaceChildren();
  addCheck('Helper 连接', false);
}

function renderStatus(status) {
  const readiness = status.readiness || {};
  const ready = status.status === 'ready';
  elements.badge.className = `badge ${ready ? 'ready' : 'degraded'}`;
  elements.badge.textContent = ready ? '可以下载' : '需要处理';
  elements.readinessTitle.textContent = ready ? '下载环境已就绪' : '下载环境未完全就绪';
  const active = (status.jobs?.queued || 0) + (status.jobs?.downloading || 0);
  elements.readinessSummary.textContent = `Helper ${status.version} · ${active} 个活动任务 · ${status.candidateCount || 0} 个候选`;
  elements.readinessList.replaceChildren();
  addCheck(`API 版本 ${status.apiVersion}`, true);
  addCheck(readiness.ffmpegReady ? `FFmpeg：${readiness.ffmpegPath}` : 'FFmpeg 不可用，请安装或检查路径', Boolean(readiness.ffmpegReady));
  addCheck(readiness.downloadDirWritable ? `下载目录：${readiness.downloadDir}` : '下载目录不可写', Boolean(readiness.downloadDirWritable));
  addCheck(readiness.persistenceEnabled ? '任务与候选会在重启后恢复' : '状态持久化未启用', Boolean(readiness.persistenceEnabled));
  addCheck(readiness.proxyConfigured ? 'Helper 已检测到代理配置' : 'Helper 使用直连网络', true);
}

function fileName(path) {
  return String(path || '').split(/[\\/]/).pop() || 'X 视频';
}

function statusText(job) {
  switch (job.status) {
    case 'queued': return '等待中';
    case 'downloading': return `下载中 ${job.progress?.speed || ''}`.trim();
    case 'completed': return '已完成';
    case 'failed': return '失败';
    case 'cancelled': return '已取消';
    default: return job.status || '未知';
  }
}

function localizedJobError(message) {
  const text = String(message || '未知错误')
    .replace(/https:\/\/video\.twimg\.com\/[^\s"']+/gi, '<视频地址>');
  if (/download interrupted because helper restarted/i.test(text)) {
    return 'Helper 重启中断了下载，可以重试';
  }
  if (/ffmpeg/i.test(text) && /not found|executable file|start/i.test(text)) {
    return 'FFmpeg 不可用，请检查安装或路径';
  }
  return text;
}

function jobButton(label, action, jobId, secondary = false) {
  const button = document.createElement('button');
  button.type = 'button';
  button.textContent = label;
  button.dataset.action = action;
  button.dataset.jobId = jobId;
  if (secondary) button.className = 'secondary';
  return button;
}

function renderJobs(jobs) {
  latestJobs = [...jobs].sort((left, right) => Date.parse(right.createdAt) - Date.parse(left.createdAt));
  elements.jobList.replaceChildren();
  const active = latestJobs.filter((job) => ['queued', 'downloading'].includes(job.status)).length;
  const completed = latestJobs.filter((job) => job.status === 'completed').length;
  elements.historySummary.textContent = `${active} 个活动 · ${completed} 个已完成`;
  if (latestJobs.length === 0) {
    const empty = document.createElement('div');
    empty.className = 'empty';
    empty.textContent = '还没有下载任务';
    elements.jobList.appendChild(empty);
    return;
  }
  for (const job of latestJobs.slice(0, 20)) {
    const row = document.createElement('article');
    row.className = 'job';
    const main = document.createElement('div');
    main.className = 'job-main';
    const name = document.createElement('span');
    name.className = 'job-name';
    name.textContent = fileName(job.outputPath) || `视频 ${job.mediaId}`;
    const status = document.createElement('span');
    status.className = `job-status ${job.status}`;
    status.textContent = statusText(job);
    main.append(name, status);
    row.appendChild(main);
    const detail = document.createElement('div');
    detail.className = 'job-detail';
    detail.textContent = job.error
      ? localizedJobError(job.error)
      : `${job.width || '?'}×${job.height || '?'} · ${new Date(job.createdAt).toLocaleString()}`;
    detail.title = detail.textContent;
    row.appendChild(detail);
    const actions = document.createElement('div');
    actions.className = 'job-actions';
    if (job.status === 'completed') {
      actions.appendChild(jobButton('显示文件', 'reveal', job.id, true));
    } else if (['failed', 'cancelled'].includes(job.status)) {
      actions.appendChild(jobButton('重新下载', 'retry', job.id));
    } else if (['queued', 'downloading'].includes(job.status)) {
      actions.appendChild(jobButton('取消', 'cancel', job.id, true));
    }
    if (actions.childElementCount) row.appendChild(actions);
    elements.jobList.appendChild(row);
  }
}

async function refreshAll() {
  showError('');
  elements.refresh.disabled = true;
  try {
    const status = await sendMessage({ type: 'helper-status' });
    renderStatus(status);
    renderJobs(await sendMessage({ type: 'job-list' }));
  } catch (error) {
    renderDisconnected(error.message);
    showError(error);
  } finally {
    elements.refresh.disabled = false;
  }
}

async function load() {
  try {
    const [settings, preferences] = await Promise.all([
      sendMessage({ type: 'helper-settings-get' }),
      chrome.storage.local.get('downloadNotifications'),
    ]);
    elements.helperUrl.value = settings.baseUrl;
    elements.helperToken.value = settings.token;
    elements.notifications.checked = preferences.downloadNotifications !== false;
    if (!settings.token) {
      elements.settings.open = true;
      renderDisconnected('请填写 Helper 地址和配对令牌');
      return;
    }
    await refreshAll();
  } catch (error) {
    renderDisconnected(error.message);
    showError(error);
  }
}

elements.refresh.addEventListener('click', refreshAll);

elements.openSettings.addEventListener('click', () => {
  chrome.runtime.openOptionsPage().catch(showError);
});

elements.testHelper.addEventListener('click', async () => {
  showError('');
  elements.testHelper.disabled = true;
  elements.connectionStatus.textContent = '检查中…';
  try {
    const status = await sendMessage({
      type: 'helper-settings-test-and-save',
      baseUrl: elements.helperUrl.value,
      token: elements.helperToken.value,
    });
    elements.connectionStatus.textContent = '已保存';
    renderStatus(status);
    renderJobs(await sendMessage({ type: 'job-list' }));
  } catch (error) {
    elements.connectionStatus.textContent = '连接失败';
    renderDisconnected(error.message);
    showError(error);
  } finally {
    elements.testHelper.disabled = false;
  }
});

elements.notifications.addEventListener('change', () => {
  chrome.storage.local.set({ downloadNotifications: elements.notifications.checked }).catch(() => {});
});

elements.jobList.addEventListener('click', async (event) => {
  const button = event.target.closest?.('button[data-action]');
  if (!button) return;
  const job = latestJobs.find((item) => item.id === button.dataset.jobId);
  if (!job) return;
  button.disabled = true;
  showError('');
  try {
    if (button.dataset.action === 'reveal') {
      await sendMessage({ type: 'job-reveal', jobId: job.id });
    } else if (button.dataset.action === 'retry') {
      await sendMessage({ type: 'job-create', candidateId: job.candidateId, variantId: job.variantId });
      await refreshAll();
    } else if (button.dataset.action === 'cancel') {
      await sendMessage({ type: 'job-cancel', jobId: job.id });
      await refreshAll();
    }
  } catch (error) {
    showError(error);
  } finally {
    button.disabled = false;
  }
});

load();
