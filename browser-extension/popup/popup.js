'use strict';

const elements = {
  helperUrl: document.querySelector('#helper-url'),
  helperToken: document.querySelector('#helper-token'),
  duration: document.querySelector('#duration'),
  testHelper: document.querySelector('#test-helper'),
  connectionStatus: document.querySelector('#connection-status'),
  start: document.querySelector('#start'),
  finish: document.querySelector('#finish'),
  statusTitle: document.querySelector('#status-title'),
  statusDetails: document.querySelector('#status-details'),
  reportSection: document.querySelector('#report-section'),
  reportSummary: document.querySelector('#report-summary'),
  reportJson: document.querySelector('#report-json'),
  error: document.querySelector('#error'),
};

let pollTimer = null;

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

function renderState(state) {
  const active = state?.status === 'active';
  elements.start.disabled = active;
  elements.finish.disabled = !active;

  if (!state) {
    elements.statusTitle.textContent = '尚未运行';
    elements.statusDetails.textContent = '在 X 页面播放视频后启动诊断。';
    return;
  }

  if (active) {
    const remaining = Math.max(0, Math.ceil((state.endsAt - Date.now()) / 1000));
    elements.statusTitle.textContent = `正在捕获，还剩 ${remaining} 秒`;
    elements.statusDetails.textContent = `观察 ${state.observationCount || 0} 次，唯一 playlist ${state.uniquePlaylistCount || 0} 个${state.lastError ? `；${state.lastError}` : ''}`;
    schedulePoll();
  } else if (state.status === 'finished') {
    elements.statusTitle.textContent = '诊断完成';
    elements.statusDetails.textContent = `观察 ${state.report?.observationCount || 0} 次，唯一 playlist ${state.report?.uniquePlaylistCount || 0} 个`;
    renderReport(state.report);
  }
}

function renderReport(report) {
  if (!report) {
    return;
  }
  elements.reportSection.hidden = false;
  const mediaCount = Array.isArray(report.media) ? report.media.length : 0;
  elements.reportSummary.textContent = `Master：${report.masterDetected ? '已发现' : '未发现'}；媒体 ID：${mediaCount} 个；探测失败：${report.failedProbeCount || 0} 个。`;
  elements.reportJson.textContent = JSON.stringify(report, null, 2);
}

function schedulePoll() {
  clearTimeout(pollTimer);
  pollTimer = setTimeout(async () => {
    try {
      renderState(await sendMessage({ type: 'diagnostic-status' }));
    } catch (error) {
      showError(error);
    }
  }, 1000);
}

async function load() {
  try {
    const settings = await sendMessage({ type: 'helper-settings-get' });
    elements.helperUrl.value = settings.baseUrl;
    elements.helperToken.value = settings.token;
    renderState(await sendMessage({ type: 'diagnostic-status' }));
  } catch (error) {
    showError(error);
  }
}

elements.testHelper.addEventListener('click', async () => {
  showError('');
  elements.connectionStatus.textContent = '连接中…';
  try {
    const health = await sendMessage({
      type: 'helper-settings-test-and-save',
      baseUrl: elements.helperUrl.value,
      token: elements.helperToken.value,
    });
    elements.connectionStatus.textContent = `已保存 · 正常 · ${health.version}`;
  } catch (error) {
    elements.connectionStatus.textContent = '连接失败';
    showError(error);
  }
});

elements.start.addEventListener('click', async () => {
  showError('');
  elements.reportSection.hidden = true;
  try {
    await sendMessage({
      type: 'helper-settings-save',
      baseUrl: elements.helperUrl.value,
      token: elements.helperToken.value,
    });
    const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
    const state = await sendMessage({
      type: 'diagnostic-start',
      tabId: tab?.id,
      durationSeconds: Number(elements.duration.value),
    });
    renderState(state);
  } catch (error) {
    showError(error);
  }
});

elements.finish.addEventListener('click', async () => {
  showError('');
  try {
    renderState(await sendMessage({ type: 'diagnostic-finish' }));
  } catch (error) {
    showError(error);
  }
});

load();
