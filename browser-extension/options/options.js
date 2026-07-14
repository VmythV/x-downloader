'use strict';

const elements = {
  badge: document.querySelector('#helper-badge'),
  downloadDir: document.querySelector('#download-dir'),
  pickDirectory: document.querySelector('#pick-directory'),
  saveDirectory: document.querySelector('#save-directory'),
  resetDirectory: document.querySelector('#reset-directory'),
  directoryStatus: document.querySelector('#directory-status'),
  filenameTemplate: document.querySelector('#filename-template'),
  concurrency: document.querySelector('#download-concurrency'),
  retryCount: document.querySelector('#retry-count'),
  saveRules: document.querySelector('#save-rules'),
  resetRules: document.querySelector('#reset-rules'),
  rulesStatus: document.querySelector('#rules-status'),
  enabled: document.querySelector('#download-enabled'),
  notifications: document.querySelector('#download-notifications'),
  helperUrl: document.querySelector('#helper-url'),
  helperToken: document.querySelector('#helper-token'),
  saveConnection: document.querySelector('#save-connection'),
  connectionStatus: document.querySelector('#connection-status'),
  error: document.querySelector('#page-error'),
};

let defaultDownloadDir = '';
let defaultFilenameTemplate = '';
let defaultConcurrency = 1;
let defaultRetryCount = 1;

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

function renderConnection(status) {
  const ready = status?.status === 'ready';
  elements.badge.className = `badge ${ready ? 'ready' : 'degraded'}`;
  elements.badge.textContent = ready ? '可以下载' : '需要处理';
  elements.connectionStatus.textContent = ready ? `Helper ${status.version} 已连接` : 'Helper 已连接，但环境未完全就绪';
  elements.connectionStatus.className = `status-text ${ready ? 'success' : ''}`;
}

function renderDisconnected(message = '未连接') {
  elements.badge.className = 'badge offline';
  elements.badge.textContent = '未连接';
  elements.connectionStatus.textContent = message;
  elements.connectionStatus.className = 'status-text';
}

function renderSettings(settings) {
  elements.downloadDir.value = settings.downloadDir || '';
  defaultDownloadDir = settings.defaultDownloadDir || settings.downloadDir || '';
  elements.filenameTemplate.value = settings.filenameTemplate || '';
  elements.concurrency.value = String(settings.concurrency || 1);
  elements.retryCount.value = String(Number.isInteger(settings.retryCount) ? settings.retryCount : 1);
  defaultFilenameTemplate = settings.defaultFilenameTemplate || settings.filenameTemplate || '';
  defaultConcurrency = settings.defaultConcurrency || settings.concurrency || 1;
  defaultRetryCount = Number.isInteger(settings.defaultRetryCount) ? settings.defaultRetryCount : (settings.retryCount || 0);
  elements.directoryStatus.textContent = '当前目录已保存，新任务会自动使用';
  elements.directoryStatus.className = 'status-text success';
  elements.rulesStatus.textContent = '下载规则已保存';
  elements.rulesStatus.className = 'status-text success';
}

async function refreshHelperSettings() {
  const [status, settings] = await Promise.all([
    sendMessage({ type: 'helper-status' }),
    sendMessage({ type: 'app-settings-get' }),
  ]);
  renderConnection(status);
  renderSettings(settings);
}

async function load() {
  try {
    const [connection, preferences] = await Promise.all([
      sendMessage({ type: 'helper-settings-get' }),
      chrome.storage.local.get(['downloadNotifications', 'downloadEnabled']),
    ]);
    elements.helperUrl.value = connection.baseUrl;
    elements.helperToken.value = connection.token;
    elements.notifications.checked = preferences.downloadNotifications !== false;
    elements.enabled.checked = preferences.downloadEnabled !== false;
    if (!connection.token) {
      renderDisconnected('请先填写 Helper 地址和配对令牌');
      return;
    }
    await refreshHelperSettings();
  } catch (error) {
    renderDisconnected(error.message);
    showError(error);
  }
}

elements.downloadDir.addEventListener('input', () => {
  elements.directoryStatus.textContent = '目录尚未保存';
  elements.directoryStatus.className = 'status-text';
});

elements.pickDirectory.addEventListener('click', async () => {
  showError('');
  elements.pickDirectory.disabled = true;
  elements.directoryStatus.textContent = '等待选择文件夹…';
  elements.directoryStatus.className = 'status-text';
  try {
    const result = await sendMessage({ type: 'app-settings-pick-download-directory' });
    if (result.cancelled) {
      elements.directoryStatus.textContent = '已取消选择，目录未更改';
      return;
    }
    elements.downloadDir.value = result.downloadDir;
    elements.directoryStatus.textContent = '已选择，点击“保存目录”后生效';
  } catch (error) {
    showError(error);
    elements.directoryStatus.textContent = '无法打开文件夹选择器';
  } finally {
    elements.pickDirectory.disabled = false;
  }
});

elements.saveDirectory.addEventListener('click', async () => {
  showError('');
  elements.saveDirectory.disabled = true;
  elements.directoryStatus.textContent = '正在保存…';
  try {
    renderSettings(await sendMessage({
      type: 'app-settings-update',
      downloadDir: elements.downloadDir.value,
    }));
  } catch (error) {
    showError(error);
    elements.directoryStatus.textContent = '保存失败';
    elements.directoryStatus.className = 'status-text';
  } finally {
    elements.saveDirectory.disabled = false;
  }
});

elements.resetDirectory.addEventListener('click', async () => {
  if (!defaultDownloadDir) return;
  elements.downloadDir.value = defaultDownloadDir;
  elements.saveDirectory.click();
});

for (const element of [elements.filenameTemplate, elements.concurrency, elements.retryCount]) {
  element.addEventListener('input', () => {
    elements.rulesStatus.textContent = '下载规则尚未保存';
    elements.rulesStatus.className = 'status-text';
  });
}

elements.saveRules.addEventListener('click', async () => {
  showError('');
  elements.saveRules.disabled = true;
  elements.rulesStatus.textContent = '正在保存…';
  try {
    renderSettings(await sendMessage({
      type: 'app-settings-update',
      filenameTemplate: elements.filenameTemplate.value,
      concurrency: Number(elements.concurrency.value),
      retryCount: Number(elements.retryCount.value),
    }));
  } catch (error) {
    showError(error);
    elements.rulesStatus.textContent = '保存失败';
    elements.rulesStatus.className = 'status-text';
  } finally {
    elements.saveRules.disabled = false;
  }
});

elements.resetRules.addEventListener('click', () => {
  elements.filenameTemplate.value = defaultFilenameTemplate;
  elements.concurrency.value = String(defaultConcurrency);
  elements.retryCount.value = String(defaultRetryCount);
  elements.saveRules.click();
});

elements.saveConnection.addEventListener('click', async () => {
  showError('');
  elements.saveConnection.disabled = true;
  elements.connectionStatus.textContent = '正在检查…';
  try {
    const status = await sendMessage({
      type: 'helper-settings-test-and-save',
      baseUrl: elements.helperUrl.value,
      token: elements.helperToken.value,
    });
    renderConnection(status);
    renderSettings(await sendMessage({ type: 'app-settings-get' }));
  } catch (error) {
    renderDisconnected('连接失败');
    showError(error);
  } finally {
    elements.saveConnection.disabled = false;
  }
});

elements.notifications.addEventListener('change', () => {
  chrome.storage.local.set({ downloadNotifications: elements.notifications.checked }).catch(showError);
});

elements.enabled.addEventListener('change', () => {
  chrome.storage.local.set({ downloadEnabled: elements.enabled.checked }).catch(showError);
});

load();
