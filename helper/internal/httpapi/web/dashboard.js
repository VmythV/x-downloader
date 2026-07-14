'use strict';

const state = {
  token: sessionStorage.getItem('xDownloaderToken') || '',
  liveJobs: [],
  jobs: [],
  jobsCursor: '',
  tags: [],
  historyCursor: '',
  refreshTimer: null,
  initialized: false,
};

const $ = (selector) => document.querySelector(selector);
const elements = {
  auth: $('#auth-panel'), token: $('#token'), connect: $('#connect'), badge: $('#connection-badge'),
  refresh: $('#refresh'), refreshInterval: $('#refresh-interval'), message: $('#message'),
  summary: $('#summary-cards'), statsTime: $('#stats-time'), daily: $('#daily-chart'), errors: $('#error-ranking'),
  authors: $('#author-ranking'), tagRanking: $('#tag-ranking'), resolutions: $('#resolution-ranking'),
  activeJobs: $('#active-jobs'), activeCount: $('#active-count'), taskFilter: $('#task-filter'), taskQuery: $('#task-query'),
  jobs: $('#jobs'), jobsMore: $('#jobs-more'), taskStatus: $('#task-status'),
  historyForm: $('#history-filter'), historyQuery: $('#history-query'), historyStatus: $('#history-status'),
  historyTag: $('#history-tag'), history: $('#history'), historyMore: $('#history-more'),
  downloadDir: $('#download-dir'), filenameTemplate: $('#filename-template'), concurrency: $('#concurrency'),
  retryCount: $('#retry-count'), pickDirectory: $('#pick-directory'), saveSettings: $('#save-settings'),
  settingsStatus: $('#settings-status'), tagForm: $('#tag-form'), tagName: $('#tag-name'),
  tagColor: $('#tag-color'), tags: $('#tags'),
};

function showMessage(text, error = false) {
  elements.message.textContent = text;
  elements.message.className = `message show${error ? ' error' : ''}`;
  clearTimeout(showMessage.timer);
  showMessage.timer = setTimeout(() => { elements.message.className = 'message'; }, 3200);
}

async function api(path, options = {}) {
  if (!state.token) throw new Error('请先输入配对令牌');
  const response = await fetch(path, {
    ...options,
    cache: 'no-store',
    headers: {
      Authorization: `Bearer ${state.token}`,
      ...(options.body ? { 'Content-Type': 'application/json' } : {}),
      ...(options.headers || {}),
    },
  });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    if (response.status === 401) disconnect();
    throw new Error(body.error || `请求失败（${response.status}）`);
  }
  return body;
}

function disconnect() {
  state.token = '';
  sessionStorage.removeItem('xDownloaderToken');
  elements.auth.classList.remove('connected');
  elements.badge.textContent = '未连接';
  elements.badge.className = 'badge danger';
}

function formatNumber(value) { return new Intl.NumberFormat('zh-CN').format(value || 0); }
function formatBytes(value) {
  if (!value) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / (1024 ** index)).toFixed(index ? 1 : 0)} ${units[index]}`;
}
function formatDate(value) { return value ? new Date(value).toLocaleString() : '—'; }
function fileName(path) { return String(path || '').split(/[\\/]/).pop() || ''; }
function escapeHTML(value) {
  return String(value ?? '').replace(/[&<>'"]/g, (char) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' })[char]);
}
function safePostURL(value) {
  try {
    const parsed = new URL(value);
    return parsed.protocol === 'https:' && ['x.com', 'www.x.com', 'twitter.com', 'www.twitter.com'].includes(parsed.hostname)
      ? parsed.href : '';
  } catch { return ''; }
}
const statusLabels = { queued: '等待中', downloading: '下载中', completed: '已完成', failed: '失败', cancelled: '已取消' };
const phaseLabels = { queued: '等待中', preparing: '准备媒体', downloading: '下载中', finalizing: '正在保存', completed: '已完成' };
const errorLabels = {
  helper_interrupted: 'Helper 重启中断', finalization_failed: '保存文件失败', ffmpeg_failed: 'FFmpeg 失败',
  playlist_expired: '播放列表过期', network: '网络错误', cancelled: '用户取消', unknown: '其他错误',
};

function localizedJobError(message) {
  const text = String(message || '').replace(/https:\/\/video\.twimg\.com\/[^\s"']+/gi, '<视频地址>');
  if (/download interrupted because helper restarted/i.test(text)) return 'Helper 重启中断了下载，可以重试';
  if (/download resumed after helper restart/i.test(text)) return 'Helper 重启后已恢复排队';
  if (/ffmpeg/i.test(text) && /not found|executable file|start/i.test(text)) return 'FFmpeg 不可用，请检查安装或路径';
  return text;
}

function renderSummary(statistics) {
  const summary = statistics.summary || {};
  const cards = [
    ['历史条目', summary.historyItems], ['视频', summary.mediaItems], ['任务', summary.jobs],
    ['已完成', summary.completed], ['成功率', `${Number(summary.successRate || 0).toFixed(1)}%`],
    ['已下载', formatBytes(summary.totalBytes)],
  ];
  elements.summary.innerHTML = cards.map(([label, value]) => `<div class="metric"><span>${label}</span><strong>${escapeHTML(value)}</strong></div>`).join('');
  elements.statsTime.textContent = `更新于 ${formatDate(statistics.generatedAt)}`;

  const daily = statistics.daily || [];
  const max = Math.max(1, ...daily.map((item) => item.completed + item.failed));
  elements.daily.innerHTML = daily.length ? daily.map((item) => {
    const total = item.completed + item.failed;
    const height = Math.max(2, total / max * 125);
    return `<div class="bar-column" title="${escapeHTML(item.date)}：完成 ${item.completed}，失败 ${item.failed}"><div class="bar" style="height:${height}px"></div><span class="bar-label">${escapeHTML(item.date.slice(5))}</span></div>`;
  }).join('') : '<span class="empty">最近 30 天暂无完成任务</span>';
  renderRanking(elements.errors, (statistics.errors || []).map((item) => ({ ...item, name: errorLabels[item.name] || item.name })));
  renderRanking(elements.authors, statistics.authors || []);
  renderRanking(elements.tagRanking, statistics.tags || []);
  renderRanking(elements.resolutions, statistics.resolutions || []);
}

function renderRanking(container, items) {
  container.className = `ranking${items.length ? '' : ' empty'}`;
  container.innerHTML = items.length
    ? items.map((item) => `<div class="rank-row"><span>${escapeHTML(item.name)}</span><strong>${formatNumber(item.count)}</strong></div>`).join('')
    : '暂无数据';
}

function progressMarkup(job) {
  if (!['queued', 'downloading'].includes(job.status)) return '';
  const percent = Math.max(0, Math.min(100, Number(job.progress?.percent || 0)));
  const indeterminate = job.status === 'downloading' && percent <= 0;
  const detail = percent > 0
    ? `${percent.toFixed(1)}%${job.progress?.speed ? ` · ${job.progress.speed}` : ''}`
    : (job.status === 'queued' ? '等待下载槽位' : `${job.progress?.outTimeSeconds?.toFixed?.(1) || 0}s${job.progress?.speed ? ` · ${job.progress.speed}` : ''}`);
  const phase = job.progress?.phase || job.status;
  return `<div class="progress-track${indeterminate ? ' indeterminate' : ''}"><div class="progress-value" style="${indeterminate ? '' : `width:${percent}%`}"></div></div><div class="job-meta">${escapeHTML(detail)} · ${escapeHTML(phaseLabels[phase] || phase)}</div>`;
}

function jobMarkup(job) {
  const actions = job.status === 'completed'
    ? `<button class="secondary" type="button" data-job-action="reveal" data-job-id="${escapeHTML(job.id)}">显示文件</button>`
    : (['failed', 'cancelled'].includes(job.status)
      ? `<button type="button" data-job-action="retry" data-job-id="${escapeHTML(job.id)}">重新下载</button>`
      : (['queued', 'downloading'].includes(job.status)
        ? `<button class="secondary" type="button" data-job-action="cancel" data-job-id="${escapeHTML(job.id)}">取消</button>` : ''));
  return `<div class="job">
    <div class="job-head"><div class="job-title" title="${escapeHTML(job.outputPath)}">${escapeHTML(fileName(job.outputPath) || `视频 ${job.mediaId}`)}</div><span class="status ${escapeHTML(job.status)}">${escapeHTML(statusLabels[job.status] || job.status)}</span></div>
    <div class="job-meta">${job.width || '?'}×${job.height || '?'} · 尝试 ${job.attempt || 0}/${job.maxAttempts || 1} · ${formatDate(job.createdAt)}</div>
    ${progressMarkup(job)}
    ${job.error ? `<div class="${job.status === 'failed' ? 'job-error' : 'job-meta'}">${escapeHTML(localizedJobError(job.error))}</div>` : ''}
    ${actions ? `<div class="job-actions">${actions}</div>` : ''}
  </div>`;
}

function renderJobs() {
  const active = state.liveJobs.filter((job) => ['queued', 'downloading'].includes(job.status));
  elements.activeCount.textContent = `${active.length} 个`;
  elements.activeJobs.className = `job-list${active.length ? '' : ' empty'}`;
  elements.activeJobs.innerHTML = active.length ? active.map(jobMarkup).join('') : '当前没有活动任务';

  const liveById = new Map(state.liveJobs.map((job) => [job.id, job]));
  const items = state.jobs.map((job) => liveById.get(job.id) || job);
  elements.jobs.className = `job-list${items.length ? '' : ' empty'}`;
  elements.jobs.innerHTML = items.length ? items.map(jobMarkup).join('') : '暂无符合条件的任务';
}

async function loadJobs(append = false) {
  const params = new URLSearchParams({ limit: '100' });
  if (elements.taskQuery.value.trim()) params.set('query', elements.taskQuery.value.trim());
  if (elements.taskStatus.value) params.set('status', elements.taskStatus.value);
  if (append && state.jobsCursor) params.set('cursor', state.jobsCursor);
  const page = await api(`/v1/job-history?${params}`);
  state.jobs = append ? [...state.jobs, ...(page.items || [])] : (page.items || []);
  state.jobsCursor = page.nextCursor || '';
  elements.jobsMore.classList.toggle('hidden', !page.hasMore);
  renderJobs();
}

function renderTags() {
  elements.tags.className = `tag-list${state.tags.length ? '' : ' empty'}`;
  elements.tags.innerHTML = state.tags.length ? state.tags.map((tag) => `<div class="tag-row"><span class="tag-dot" style="background:${escapeHTML(tag.color)}"></span><span>${escapeHTML(tag.name)}</span><button class="secondary" type="button" data-edit-tag="${tag.id}" data-tag-name="${escapeHTML(tag.name)}" data-tag-color="${escapeHTML(tag.color)}">编辑</button><button class="danger" type="button" data-delete-tag="${tag.id}">删除</button></div>`).join('') : '暂无标签';
  const selected = elements.historyTag.value;
  elements.historyTag.innerHTML = '<option value="">全部标签</option>' + state.tags.map((tag) => `<option value="${tag.id}">${escapeHTML(tag.name)}</option>`).join('');
  elements.historyTag.value = selected;
}

function renderHistory(items, append = false) {
  const markup = items.map((item) => {
    const chips = (item.tags || []).map((tag) => `<span class="tag-chip" style="background:${escapeHTML(tag.color)}">${escapeHTML(tag.name)}<button type="button" title="移除标签" data-remove-tag="${tag.id}" data-history-id="${item.id}">×</button></span>`).join('');
    const available = state.tags.filter((tag) => !(item.tags || []).some((current) => current.id === tag.id));
    const postURL = safePostURL(item.postURL);
    return `<div class="history-item" data-history-id="${item.id}">
      <div class="history-head"><div class="history-title">${escapeHTML(item.author ? `@${item.author}` : '未知作者')}${item.postId ? ` · ${escapeHTML(item.postId)}` : ''}</div><span class="status ${escapeHTML(item.latestStatus)}">${escapeHTML(statusLabels[item.latestStatus] || '未下载')}</span></div>
      <div class="history-meta">${item.mediaCount} 个视频 · ${item.jobCount} 个任务 · 最近检测 ${formatDate(item.lastSeenAt)}</div>
      <div>${chips}</div>
      <div class="history-actions">
        <button class="secondary" type="button" data-note="${item.id}" data-note-value="${escapeHTML(item.note || '')}">${item.note ? '编辑备注' : '添加备注'}</button>
        ${available.length ? `<select data-tag-select="${item.id}"><option value="">添加标签…</option>${available.map((tag) => `<option value="${tag.id}">${escapeHTML(tag.name)}</option>`).join('')}</select>` : ''}
        ${postURL ? `<a href="${escapeHTML(postURL)}" target="_blank" rel="noreferrer">打开帖子</a>` : ''}
        <button class="danger" type="button" data-delete-history="${item.id}">删除历史</button>
      </div>
      ${item.note ? `<div class="job-meta">备注：${escapeHTML(item.note)}</div>` : ''}
    </div>`;
  }).join('');
  if (append) elements.history.insertAdjacentHTML('beforeend', markup);
  else elements.history.innerHTML = markup || '暂无符合条件的历史';
  elements.history.className = `history-list${elements.history.querySelector('.history-item') ? '' : ' empty'}`;
}

async function loadHistory(append = false) {
  const params = new URLSearchParams({ limit: '50' });
  if (elements.historyQuery.value.trim()) params.set('query', elements.historyQuery.value.trim());
  if (elements.historyStatus.value) params.set('status', elements.historyStatus.value);
  if (elements.historyTag.value) params.set('tagId', elements.historyTag.value);
  if (append && state.historyCursor) params.set('cursor', state.historyCursor);
  const page = await api(`/v1/history?${params}`);
  renderHistory(page.items || [], append);
  state.historyCursor = page.nextCursor || '';
  elements.historyMore.classList.toggle('hidden', !page.hasMore);
}

function renderSettings(settings) {
  elements.downloadDir.value = settings.downloadDir || '';
  elements.filenameTemplate.value = settings.filenameTemplate || '';
  elements.concurrency.value = String(settings.concurrency || 1);
  elements.retryCount.value = String(settings.retryCount ?? 1);
}

async function refreshAll(silent = false) {
  if (!state.token) return;
  elements.refresh.disabled = true;
  try {
    const fullRefresh = !silent || !state.initialized;
    const requests = [api('/v1/status'), api('/v1/jobs'), api('/v1/statistics'), api('/v1/tags')];
    if (fullRefresh) requests.push(api('/v1/settings'));
    const [status, jobs, statistics, tags, settings] = await Promise.all(requests);
    state.liveJobs = jobs.sort((a, b) => new Date(b.createdAt) - new Date(a.createdAt));
    state.tags = tags;
    elements.auth.classList.add('connected');
    elements.badge.textContent = status.status === 'ready' ? '运行正常' : '需要处理';
    elements.badge.className = `badge${status.status === 'ready' ? '' : ' danger'}`;
    renderSummary(statistics);
    renderJobs();
    renderTags();
    if (fullRefresh) {
      renderSettings(settings);
      await Promise.all([loadHistory(false), loadJobs(false)]);
    }
    state.initialized = true;
    if (!silent) showMessage('数据已刷新');
  } catch (error) {
    if (!silent) showMessage(error.message, true);
  } finally {
    elements.refresh.disabled = false;
  }
}

function configureAutoRefresh() {
  clearInterval(state.refreshTimer);
  localStorage.setItem('xDownloaderRefreshInterval', elements.refreshInterval.value);
  const seconds = Number(elements.refreshInterval.value);
  if (seconds > 0) state.refreshTimer = setInterval(() => refreshAll(true), seconds * 1000);
}

document.querySelectorAll('.tab').forEach((tab) => tab.addEventListener('click', () => {
  document.querySelectorAll('.tab').forEach((item) => item.classList.toggle('active', item === tab));
  document.querySelectorAll('.view').forEach((view) => view.classList.toggle('active', view.id === `view-${tab.dataset.view}`));
}));

elements.connect.addEventListener('click', async () => {
  state.token = elements.token.value.trim();
  sessionStorage.setItem('xDownloaderToken', state.token);
  await refreshAll();
});
elements.token.addEventListener('keydown', (event) => { if (event.key === 'Enter') elements.connect.click(); });
elements.refresh.addEventListener('click', () => refreshAll());
elements.refreshInterval.addEventListener('change', configureAutoRefresh);
elements.taskFilter.addEventListener('submit', (event) => {
  event.preventDefault();
  state.jobsCursor = '';
  loadJobs(false).catch((error) => showMessage(error.message, true));
});
elements.jobsMore.addEventListener('click', () => loadJobs(true).catch((error) => showMessage(error.message, true)));
elements.historyForm.addEventListener('submit', (event) => { event.preventDefault(); state.historyCursor = ''; loadHistory(false).catch((error) => showMessage(error.message, true)); });
elements.historyMore.addEventListener('click', () => loadHistory(true).catch((error) => showMessage(error.message, true)));

elements.pickDirectory.addEventListener('click', async () => {
  try {
    const result = await api('/v1/settings/pick-download-directory', { method: 'POST' });
    if (!result.cancelled) elements.downloadDir.value = result.downloadDir;
  } catch (error) { showMessage(error.message, true); }
});
elements.saveSettings.addEventListener('click', async () => {
  elements.saveSettings.disabled = true;
  try {
    const settings = await api('/v1/settings', { method: 'PUT', body: JSON.stringify({
      downloadDir: elements.downloadDir.value,
      filenameTemplate: elements.filenameTemplate.value,
      concurrency: Number(elements.concurrency.value),
      retryCount: Number(elements.retryCount.value),
    }) });
    renderSettings(settings);
    elements.settingsStatus.textContent = '已保存';
    showMessage('配置已保存');
  } catch (error) { showMessage(error.message, true); }
  finally { elements.saveSettings.disabled = false; }
});

elements.tagForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  try {
    await api('/v1/tags', { method: 'POST', body: JSON.stringify({ name: elements.tagName.value, color: elements.tagColor.value }) });
    elements.tagName.value = '';
    state.tags = await api('/v1/tags');
    renderTags();
    showMessage('标签已创建');
  } catch (error) { showMessage(error.message, true); }
});
elements.tags.addEventListener('click', async (event) => {
  const remove = event.target.closest('[data-delete-tag]');
  const edit = event.target.closest('[data-edit-tag]');
  if (!remove && !edit) return;
  try {
    if (remove) {
      if (!confirm('删除这个标签？历史记录不会被删除。')) return;
      await api(`/v1/tags/${remove.dataset.deleteTag}`, { method: 'DELETE' });
    } else {
      const name = prompt('标签名称', edit.dataset.tagName || '');
      if (name === null) return;
      const color = prompt('标签颜色（#RRGGBB）', edit.dataset.tagColor || '#1d9bf0');
      if (color === null) return;
      await api(`/v1/tags/${edit.dataset.editTag}`, { method: 'PATCH', body: JSON.stringify({ name, color }) });
    }
    state.tags = await api('/v1/tags');
    renderTags();
    await loadHistory(false);
  } catch (error) { showMessage(error.message, true); }
});
elements.history.addEventListener('change', async (event) => {
  const select = event.target.closest('[data-tag-select]');
  if (!select || !select.value) return;
  try {
    await api(`/v1/history/${select.dataset.tagSelect}/tags/${select.value}`, { method: 'PUT' });
    await loadHistory(false);
  } catch (error) { showMessage(error.message, true); }
});
elements.history.addEventListener('click', async (event) => {
  const remove = event.target.closest('[data-remove-tag]');
  const note = event.target.closest('[data-note]');
  const removeHistory = event.target.closest('[data-delete-history]');
  try {
    if (remove) {
      await api(`/v1/history/${remove.dataset.historyId}/tags/${remove.dataset.removeTag}`, { method: 'DELETE' });
      await loadHistory(false);
    } else if (note) {
      const value = prompt('输入备注（留空可清除）', note.dataset.noteValue || '');
      if (value === null) return;
      await api(`/v1/history/${note.dataset.note}`, { method: 'PATCH', body: JSON.stringify({ note: value }) });
      await loadHistory(false);
    } else if (removeHistory && confirm('只删除这条历史和关联任务记录，不会删除已下载视频。继续吗？')) {
      await api(`/v1/history/${removeHistory.dataset.deleteHistory}`, { method: 'DELETE' });
      await refreshAll(true);
    }
  } catch (error) { showMessage(error.message, true); }
});

elements.jobs.addEventListener('click', async (event) => {
  const button = event.target.closest('[data-job-action]');
  if (!button) return;
  const job = [...state.liveJobs, ...state.jobs].find((item) => item.id === button.dataset.jobId);
  if (!job) return;
  button.disabled = true;
  try {
    if (button.dataset.jobAction === 'reveal') {
      await api(`/v1/jobs/${job.id}/reveal`, { method: 'POST' });
    } else if (button.dataset.jobAction === 'cancel') {
      await api(`/v1/jobs/${job.id}`, { method: 'DELETE' });
    } else if (button.dataset.jobAction === 'retry') {
      await api('/v1/jobs', { method: 'POST', body: JSON.stringify({ candidateId: job.candidateId, variantId: job.variantId }) });
    }
    await refreshAll(false);
  } catch (error) { showMessage(error.message, true); }
  finally { button.disabled = false; }
});

elements.refreshInterval.value = localStorage.getItem('xDownloaderRefreshInterval') || '5';
configureAutoRefresh();
if (state.token) refreshAll(true);
else elements.token.focus();
