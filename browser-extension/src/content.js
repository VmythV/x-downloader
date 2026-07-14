(function installMediaBridgeAndTray() {
  'use strict';

  const EVENT_NAME = 'x-downloader:hls-master';
  const DISPLAY_MODE_KEY = 'mediaDisplayMode';
  const MAX_CANDIDATES = 250;
  const MAX_JOBS = 500;
  const MAX_KNOWN_CAPTURES = 250;
  const trayCore = globalThis.XDownloaderTray;
  if (!trayCore) {
    return;
  }
  const candidates = new Map();
  const jobs = new Map();
  const selectedVariants = new Map();
  const knownCaptures = new Map();
  const contextSignatures = new Map();
  const inlineControls = new Map();
  let floatingPickerControl = null;
  let floatingPickerHost = null;
  let floatingPickerPositionFrame = null;
  let floatingPickerRoot = null;
  let trayHost = null;
  let trayRoot = null;
  let cardContainer = null;
  let collapsed = false;
  let contextScanTimer = null;
  let jobPollTimer = null;
  let inlineRenderTimer = null;
  let displayMode = 'tray';
  let helperConnected = null;
  let helperConnectionError = '';
  let currentPageKey = trayCore.pageKey(location.href, location.href);

  function candidatesForCurrentPage() {
    return trayCore.filterCandidatesForPage(candidates.values(), location.href);
  }

  function syncCurrentPage() {
    const nextPageKey = trayCore.pageKey(location.href, location.href);
    if (nextPageKey === currentPageKey) {
      return;
    }
    currentPageKey = nextPageKey;
    renderTray();
    scheduleContextScan();
  }

  async function sendMessage(message) {
    const tracksHelper = !['job-notification'].includes(message?.type);
    try {
      const response = await chrome.runtime.sendMessage(message);
      if (!response?.ok) {
        throw new Error(response?.error || '扩展后台没有响应');
      }
      if (tracksHelper) {
        helperConnected = true;
        helperConnectionError = '';
      }
      return response.result;
    } catch (error) {
      if (tracksHelper) {
        const connectionFailure = /无法连接 Helper|Helper 响应超时|请先配置 Helper|配对令牌无效|扩展后台没有响应/.test(error.message);
        helperConnected = !connectionFailure;
        helperConnectionError = connectionFailure ? error.message : '';
      }
      throw error;
    }
  }

  function mediaIDFromAssetUrl(value) {
    try {
      const url = new URL(value, location.href);
      const match = /\/(?:amplify_video(?:_thumb)?|ext_tw_video(?:_thumb)?)\/(\d+)(?:\/|$)/.exec(url.pathname);
      return match ? match[1] : '';
    } catch {
      return '';
    }
  }

  function assetUrl(element) {
    if (element instanceof HTMLVideoElement && element.poster) {
      return element.poster;
    }
    if (element instanceof HTMLImageElement) {
      return element.currentSrc || element.src;
    }
    return element.getAttribute?.('poster') || element.getAttribute?.('src') || '';
  }

  function findMediaElement(mediaId) {
    for (const element of document.querySelectorAll('video[poster], img[src]')) {
      if (mediaIDFromAssetUrl(assetUrl(element)) === mediaId) {
        return element;
      }
    }
    return null;
  }

  function findPostLink(mediaElement, article) {
    let node = mediaElement.parentElement;
    while (node && article.contains(node)) {
      const links = [...node.querySelectorAll('a[href*="/status/"]')]
        .filter((link) => link.querySelector('time'));
      if (links.length === 1) {
        return links[0];
      }
      node = node.parentElement;
    }
    return article.querySelector('a[href*="/status/"] time')?.closest('a') || null;
  }

  function mediaIndexWithin(article, mediaId) {
    const ids = [];
    for (const element of article.querySelectorAll('video[poster], img[src]')) {
      const found = mediaIDFromAssetUrl(assetUrl(element));
      if (found && !ids.includes(found)) {
        ids.push(found);
      }
    }
    const index = ids.indexOf(mediaId);
    return index >= 0 ? index + 1 : 0;
  }

  function extractContext(mediaId) {
    const mediaElement = findMediaElement(mediaId);
    if (!mediaElement) {
      return { pageUrl: location.href };
    }
    const article = mediaElement.closest('article');
    if (!article) {
      return { pageUrl: location.href, thumbnailUrl: assetUrl(mediaElement) };
    }
    const postLink = findPostLink(mediaElement, article);
    const postUrl = postLink?.href || '';
    const postMatch = /\/([^/]+)\/status\/(\d+)/.exec(new URL(postUrl || location.href).pathname);
    const time = postLink?.querySelector('time');
    return {
      pageUrl: location.href,
      postUrl,
      postId: postMatch?.[2] || '',
      author: postMatch?.[1] || '',
      createdAt: time?.dateTime || null,
      mediaIndex: mediaIndexWithin(article, mediaId),
      thumbnailUrl: assetUrl(mediaElement),
    };
  }

  function contextSignature(context) {
    return JSON.stringify([
      context.postId, context.author, context.createdAt,
      context.mediaIndex, context.thumbnailUrl,
    ]);
  }

  function scheduleContextScan(delay = 300) {
    if (contextScanTimer) {
      return;
    }
    contextScanTimer = setTimeout(() => {
      contextScanTimer = null;
      updateKnownContexts().catch(() => {});
    }, delay);
  }

  async function updateKnownContexts() {
    for (const [mediaId, capture] of knownCaptures) {
      const context = extractContext(mediaId);
      const pending = candidates.has(`pending-${mediaId}`);
      if (!context.postId && !pending) {
        continue;
      }
      const signature = contextSignature(context);
      if (!pending && contextSignatures.get(mediaId) === signature) {
        continue;
      }
      try {
        const result = await sendMessage({
          type: 'media-context-updated',
          masterUrl: capture.masterUrl,
          context,
        });
        if (result?.candidate) {
          contextSignatures.set(mediaId, signature);
          upsertCandidate(result.candidate);
        }
      } catch {
        scheduleContextScan(5000);
      }
    }
  }

  function ensureTray() {
    if (trayRoot) {
      return;
    }
    trayHost = document.createElement('div');
    trayHost.id = 'x-downloader-media-tray';
    trayRoot = trayHost.attachShadow({ mode: 'closed' });
    trayRoot.innerHTML = `
      <style>
        :host { all: initial; }
        .tray { position: fixed; right: 16px; bottom: 16px; width: 350px; max-height: calc(100vh - 32px); z-index: 2147483647; border: 1px solid rgba(255,255,255,.16); border-radius: 14px; color: #e7e9ea; background: rgba(21,32,43,.97); box-shadow: 0 10px 35px rgba(0,0,0,.35); font: 13px/1.4 system-ui,-apple-system,sans-serif; overflow: hidden; }
        .tray.inline-mode { width: auto; min-width: 275px; }
        .header { display: flex; align-items: center; justify-content: space-between; padding: 10px 12px; background: #15202b; }
        .header strong { font-size: 14px; }
        .helper-state { display: inline-block; width: 7px; height: 7px; margin-left: 5px; border-radius: 50%; background: #8b98a5; vertical-align: 1px; }
        .helper-state.ready { background: #00ba7c; }
        .helper-state.offline { background: #f4212e; }
        .header-actions, .mode-switch { display: flex; align-items: center; gap: 4px; }
        .mode-button { border: 0; border-radius: 999px; padding: 4px 8px; color: #8b98a5; background: transparent; cursor: pointer; font-size: 11px; }
        .mode-button.active { color: white; background: #1d9bf0; }
        .toggle { border: 0; color: #e7e9ea; background: transparent; cursor: pointer; font-size: 16px; }
        .cards { max-height: min(570px, calc(100vh - 92px)); overflow-x: hidden; overflow-y: auto; overscroll-behavior: contain; padding: 8px; scrollbar-gutter: stable; }
        .post-group { margin-bottom: 9px; }
        .post-group:last-child { margin-bottom: 0; }
        .group-header { position: sticky; top: -8px; z-index: 1; display: flex; justify-content: space-between; gap: 8px; margin: -1px -1px 6px; padding: 7px 6px 5px; color: #cfd9de; background: rgba(21,32,43,.98); font-size: 12px; font-weight: 600; }
        .group-title { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
        .group-count { color: #8b98a5; font-weight: 400; white-space: nowrap; }
        .card { display: grid; grid-template-columns: 76px 1fr; gap: 9px; margin-bottom: 8px; padding: 8px; border-radius: 10px; background: #1e2732; }
        .thumb { width: 76px; height: 76px; border-radius: 7px; background: #0f1419; object-fit: cover; }
        .placeholder { display: grid; place-items: center; color: #71767b; }
        .meta { min-width: 0; }
        .title { overflow: hidden; color: #f7f9f9; font-weight: 600; text-overflow: ellipsis; white-space: nowrap; }
        .detail, .status { margin-top: 3px; color: #8b98a5; font-size: 11px; word-break: break-all; }
        select { width: 100%; margin-top: 6px; padding: 5px; border: 1px solid #536471; border-radius: 6px; color: #e7e9ea; background: #15202b; }
        .actions { display: flex; gap: 6px; margin-top: 6px; }
        button.action { flex: 1; border: 0; border-radius: 999px; padding: 6px 8px; color: white; background: #1d9bf0; cursor: pointer; }
        button.cancel { background: #536471; }
        button:disabled { cursor: default; opacity: .55; }
        .empty { padding: 18px; color: #8b98a5; text-align: center; }
        .detection-note { margin-bottom: 8px; padding: 7px 8px; border-radius: 8px; color: #8b98a5; background: rgba(255,255,255,.04); font-size: 11px; }
        .detection-note.problem { color: #ff9aa2; background: rgba(244,33,46,.09); }
        .collapsed .cards { display: none; }
        .inline-mode .cards, .inline-mode .toggle { display: none; }
      </style>
      <aside class="tray">
        <div class="header">
          <strong>X Downloader · <span class="count">0</span><span class="helper-state" title="Helper 状态"></span></strong>
          <div class="header-actions">
            <div class="mode-switch">
              <button class="mode-button" data-mode="tray" type="button">列表</button>
              <button class="mode-button" data-mode="inline" type="button">帖内</button>
            </div>
            <button class="toggle" type="button" title="折叠">−</button>
          </div>
        </div>
        <div class="cards"></div>
      </aside>`;
    cardContainer = trayRoot.querySelector('.cards');
    trayRoot.querySelector('.toggle').addEventListener('click', () => {
      collapsed = !collapsed;
      trayRoot.querySelector('.tray').classList.toggle('collapsed', collapsed);
      trayRoot.querySelector('.toggle').textContent = collapsed ? '+' : '−';
      if (!collapsed) {
        sizeTrayForFiveCards();
      }
    });
    trayRoot.addEventListener('click', handleTrayClick);
    trayRoot.addEventListener('change', handleTrayChange);
    (document.documentElement || document).appendChild(trayHost);
  }

  function createTextElement(tag, className, text) {
    const element = document.createElement(tag);
    element.className = className;
    element.textContent = text;
    return element;
  }

  function setDisplayMode(mode) {
    if (!['tray', 'inline'].includes(mode) || displayMode === mode) {
      return;
    }
    displayMode = mode;
    if (mode === 'tray') {
      collapsed = false;
    }
    chrome.storage.local.set({ [DISPLAY_MODE_KEY]: mode }).catch(() => {});
    renderTray();
  }

  function findPostActionGroup(items) {
    for (const candidate of items) {
      const mediaElement = findMediaElement(candidate.mediaId);
      const article = mediaElement?.closest('article');
      if (!article) {
        continue;
      }
      const actionGroup = [...article.querySelectorAll('[role="group"]')]
        .find(trayCore.isPostActionGroup);
      if (actionGroup) {
        return actionGroup;
      }
    }
    return null;
  }

  function removeInlineControl(groupKey) {
    const control = inlineControls.get(groupKey);
    if (!control) {
      return;
    }
    removeFloatingPicker(control);
    control.host.remove();
    inlineControls.delete(groupKey);
  }

  function clearInlineControls() {
    for (const groupKey of [...inlineControls.keys()]) {
      removeInlineControl(groupKey);
    }
  }

  function bestVariant(candidate) {
    return candidate.variants?.[0] || null;
  }

  function inlineItemState(candidate) {
    const variant = bestVariant(candidate);
    const job = variant?.id ? latestJob(candidate.id, variant.id) : null;
    return {
      candidate,
      job,
      ...trayCore.inlineDownloadState(candidate, job ? { ...job, error: localizedJobError(job.error) } : null),
    };
  }

  function inlineGroupState(items) {
    const itemStates = items.map(inlineItemState);
    const selectable = itemStates.filter((item) => item.selectable).length;
    const active = itemStates.filter((item) => item.kind === 'active').length;
    const completed = itemStates.filter((item) => item.kind === 'completed').length;
    const retry = itemStates.filter((item) => item.kind === 'retry').length;
    const unavailable = itemStates.filter((item) => item.kind === 'unavailable').length;

    if (selectable > 0) {
      return {
        disabled: false,
        items: itemStates,
        kind: retry > 0 || unavailable > 0 ? 'retry' : 'ready',
        title: items.length > 1
          ? `选择要下载的视频（${selectable} 个可选）`
          : `${retry > 0 ? '重试' : '下载'}该视频的最高画质`,
      };
    }
    if (active > 0) {
      return {
        disabled: true,
        items: itemStates,
        kind: 'active',
        title: `${active} 个视频正在下载`,
      };
    }
    if (completed > 0 && unavailable === 0) {
      return {
        disabled: true,
        items: itemStates,
        kind: 'completed',
        title: `${completed} 个视频已下载`,
      };
    }
    return {
      disabled: true,
      items: itemStates,
      kind: 'error',
      title: unavailable > 0 ? `${unavailable} 个视频暂不可下载` : '当前帖子没有可下载视频',
    };
  }

  function createInlineIcon(kind) {
    if (kind === 'active') {
      return createTextElement('span', 'spinner', '');
    }
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('viewBox', '0 0 24 24');
    svg.setAttribute('aria-hidden', 'true');
    const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
    if (kind === 'completed') {
      path.setAttribute('d', 'M9.55 18.2 3.9 12.55l1.42-1.42 4.23 4.24 9.13-9.14 1.42 1.42L9.55 18.2z');
    } else {
      path.setAttribute('d', 'M11 3h2v10.17l3.59-3.58L18 11l-6 6-6-6 1.41-1.41L11 13.17V3zM5 19h14v2H5z');
    }
    svg.appendChild(path);
    return svg;
  }

  function inlineControl(group, actionGroup) {
    let control = inlineControls.get(group.key);
    if (control && (!control.host.isConnected || control.actionGroup !== actionGroup)) {
      removeInlineControl(group.key);
      control = null;
    }
    if (!control) {
      const host = document.createElement('div');
      host.dataset.xDownloaderInline = group.key;
      host.style.cssText = 'all:initial;position:relative;display:flex;flex:0 0 auto;align-self:stretch;align-items:center;justify-content:center;overflow:visible;pointer-events:auto;';
      host.addEventListener('click', (event) => event.stopPropagation());
      host.addEventListener('pointerdown', (event) => event.stopPropagation());
      const root = host.attachShadow({ mode: 'closed' });
      actionGroup.appendChild(host);
      control = {
        actionGroup,
        group,
        host,
        open: false,
        root,
        selectedIds: new Set(),
      };
      host.addEventListener('keydown', (event) => {
        if (event.key === 'Escape' && control.open) {
          control.open = false;
          renderInlineControl(control, control.group);
        }
      });
      inlineControls.set(group.key, control);
    }
    control.group = group;
    renderInlineControl(control, group);
  }

  function closeInlinePickers(exceptGroupKey = '') {
    for (const [groupKey, control] of inlineControls) {
      if (!control.open || groupKey === exceptGroupKey) {
        continue;
      }
      control.open = false;
      renderInlineControl(control, control.group);
    }
  }

  function toggleInlinePicker(control, group, state) {
    if (control.open) {
      control.open = false;
    } else {
      closeInlinePickers(group.key);
      control.open = true;
      control.selectedIds = new Set(
        state.items
          .filter((item) => item.selectable)
          .map((item) => item.candidate.id),
      );
    }
    renderInlineControl(control, group);
  }

  function removeFloatingPicker(control = null) {
    if (control && floatingPickerControl !== control) {
      return;
    }
    if (floatingPickerPositionFrame) {
      cancelAnimationFrame(floatingPickerPositionFrame);
      floatingPickerPositionFrame = null;
    }
    floatingPickerHost?.remove();
    floatingPickerControl = null;
    floatingPickerHost = null;
    floatingPickerRoot = null;
  }

  function ensureFloatingPicker(control) {
    if (floatingPickerHost && floatingPickerControl === control) {
      return;
    }
    removeFloatingPicker();
    floatingPickerHost = document.createElement('div');
    floatingPickerHost.id = 'x-downloader-floating-picker';
    floatingPickerHost.style.cssText = 'all:initial;position:fixed;inset:0;z-index:2147483647;width:0;height:0;overflow:visible;pointer-events:none;';
    floatingPickerHost.addEventListener('click', (event) => event.stopPropagation());
    floatingPickerHost.addEventListener('pointerdown', (event) => event.stopPropagation());
    floatingPickerRoot = floatingPickerHost.attachShadow({ mode: 'closed' });
    floatingPickerControl = control;
    (document.documentElement || document).appendChild(floatingPickerHost);
  }

  function positionFloatingPicker(control, picker) {
    const gap = 8;
    const margin = 12;
    const triggerRect = control.host.getBoundingClientRect();
    const pickerWidth = Math.max(240, Math.min(300, window.innerWidth - margin * 2));
    const left = Math.min(
      Math.max(margin, triggerRect.right - pickerWidth),
      Math.max(margin, window.innerWidth - pickerWidth - margin),
    );
    picker.style.width = `${pickerWidth}px`;
    picker.style.left = `${left}px`;
    picker.style.top = `${triggerRect.bottom + gap}px`;
    picker.style.bottom = 'auto';

    const pickerRect = picker.getBoundingClientRect();
    if (pickerRect.bottom > window.innerHeight - margin && triggerRect.top > pickerRect.height + gap + margin) {
      picker.style.top = 'auto';
      picker.style.bottom = `${window.innerHeight - triggerRect.top + gap}px`;
    } else if (pickerRect.bottom > window.innerHeight - margin) {
      picker.style.top = `${Math.max(margin, window.innerHeight - pickerRect.height - margin)}px`;
    }
  }

  function floatingPickerStyle() {
    const style = document.createElement('style');
    style.textContent = `
      :host { all: initial; }
      *, *::before, *::after { box-sizing: border-box; }
      .picker { position: fixed; z-index: 2147483647; max-height: calc(100vh - 24px); overflow: hidden; border: 1px solid rgb(56,68,77); border-radius: 14px; color: rgb(231,233,234); background: rgb(21,32,43); box-shadow: 0 10px 36px rgba(0,0,0,.48); pointer-events: auto; font: 13px/1.35 system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif; }
      .picker header, .picker footer { display: flex; align-items: center; justify-content: space-between; gap: 10px; padding: 10px 12px; }
      .picker header { border-bottom: 1px solid rgb(56,68,77); }
      .select-all { display: flex; min-width: 0; align-items: center; gap: 9px; cursor: pointer; }
      input[type="checkbox"] { width: 16px; height: 16px; margin: 0; flex: 0 0 auto; accent-color: rgb(29,155,240); }
      button.close { width: 26px; height: 26px; flex: 0 0 auto; border: 0; border-radius: 999px; color: rgb(139,152,165); background: transparent; cursor: pointer; font: 22px/24px system-ui,sans-serif; }
      button.close:hover { color: white; background: rgba(255,255,255,.1); }
      .picker-list { max-height: min(270px, calc(100vh - 130px)); overflow-y: auto; overscroll-behavior: contain; padding: 4px 0; }
      .choice { display: grid; grid-template-columns: 16px 52px minmax(0,1fr); align-items: center; gap: 10px; min-height: 62px; padding: 7px 12px; cursor: pointer; }
      .choice:hover { background: rgba(255,255,255,.06); }
      .choice.unavailable, .choice.active, .choice.completed { cursor: default; }
      .choice img, .picker-placeholder { width: 52px; height: 48px; border-radius: 7px; object-fit: cover; background: rgb(15,20,25); }
      .picker-placeholder { display: grid; place-items: center; color: rgb(113,118,123); font-size: 9px; }
      .picker-meta { display: flex; min-width: 0; flex-direction: column; gap: 3px; }
      .picker-title { color: rgb(247,249,249); font-weight: 650; }
      .picker-status { overflow: hidden; color: rgb(139,152,165); font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
      .choice.completed .picker-status { color: rgb(0,186,124); }
      .choice.retry .picker-status, .choice.unavailable .picker-status { color: rgb(244,133,142); }
      .picker footer { border-top: 1px solid rgb(56,68,77); }
      .selection-count { color: rgb(139,152,165); font-size: 11px; white-space: nowrap; }
      button.confirm { min-width: 126px; border: 0; border-radius: 999px; padding: 7px 13px; color: white; background: rgb(29,155,240); cursor: pointer; font: 600 12px/1.3 system-ui,-apple-system,sans-serif; }
      button.confirm:hover:not(:disabled) { background: rgb(26,140,216); }
      button.confirm:disabled { cursor: default; opacity: .5; }
    `;
    return style;
  }

  function renderFloatingPicker(control, group, state) {
    ensureFloatingPicker(control);
    const picker = createInlinePicker(control, group, state);
    floatingPickerRoot.replaceChildren(floatingPickerStyle(), picker);
    positionFloatingPicker(control, picker);
  }

  function scheduleFloatingPickerPosition() {
    if (floatingPickerPositionFrame || !floatingPickerControl?.open || !floatingPickerRoot) {
      return;
    }
    floatingPickerPositionFrame = requestAnimationFrame(() => {
      floatingPickerPositionFrame = null;
      const picker = floatingPickerRoot?.querySelector('.picker');
      const control = floatingPickerControl;
      if (!picker || !control?.host.isConnected) {
        return;
      }
      const triggerRect = control.host.getBoundingClientRect();
      if (triggerRect.bottom <= 0 || triggerRect.top >= window.innerHeight) {
        control.open = false;
        renderInlineControl(control, control.group);
      } else {
        positionFloatingPicker(control, picker);
      }
    });
  }

  function createInlinePicker(control, group, state) {
    const selectableItems = state.items.filter((item) => item.selectable);
    const selectableIds = new Set(selectableItems.map((item) => item.candidate.id));
    for (const candidateId of [...control.selectedIds]) {
      if (!selectableIds.has(candidateId)) {
        control.selectedIds.delete(candidateId);
      }
    }

    const picker = document.createElement('section');
    picker.className = 'picker';
    picker.setAttribute('role', 'dialog');
    picker.setAttribute('aria-label', '选择要下载的视频');

    const header = document.createElement('header');
    const selectAllLabel = document.createElement('label');
    selectAllLabel.className = 'select-all';
    const selectAll = document.createElement('input');
    selectAll.type = 'checkbox';
    selectAll.disabled = selectableItems.length === 0;
    selectAll.checked = selectableItems.length > 0
      && selectableItems.every((item) => control.selectedIds.has(item.candidate.id));
    selectAll.indeterminate = control.selectedIds.size > 0 && !selectAll.checked;
    selectAll.addEventListener('change', () => {
      control.selectedIds = selectAll.checked
        ? new Set(selectableItems.map((item) => item.candidate.id))
        : new Set();
      renderInlineControl(control, group);
    });
    selectAllLabel.appendChild(selectAll);
    selectAllLabel.appendChild(createTextElement('strong', '', `选择视频（${state.items.length}）`));
    header.appendChild(selectAllLabel);
    const close = createTextElement('button', 'close', '×');
    close.type = 'button';
    close.title = '关闭';
    close.setAttribute('aria-label', '关闭视频选择');
    close.addEventListener('click', () => {
      control.open = false;
      renderInlineControl(control, group);
    });
    header.appendChild(close);
    picker.appendChild(header);

    const list = document.createElement('div');
    list.className = 'picker-list';
    state.items.forEach((item, index) => {
      const choice = document.createElement('label');
      choice.className = `choice ${item.kind}`;
      const checkbox = document.createElement('input');
      checkbox.type = 'checkbox';
      checkbox.disabled = !item.selectable;
      checkbox.checked = item.selectable && control.selectedIds.has(item.candidate.id);
      checkbox.addEventListener('change', () => {
        if (checkbox.checked) {
          control.selectedIds.add(item.candidate.id);
        } else {
          control.selectedIds.delete(item.candidate.id);
        }
        renderInlineControl(control, group);
      });
      choice.appendChild(checkbox);

      const thumbnailUrl = item.candidate.context?.thumbnailUrl;
      if (thumbnailUrl) {
        const image = document.createElement('img');
        image.src = thumbnailUrl;
        image.alt = '';
        choice.appendChild(image);
      } else {
        choice.appendChild(createTextElement('span', 'picker-placeholder', 'VIDEO'));
      }

      const meta = document.createElement('span');
      meta.className = 'picker-meta';
      meta.appendChild(createTextElement(
        'span',
        'picker-title',
        `视频 ${item.candidate.context?.mediaIndex || index + 1}`,
      ));
      const status = createTextElement('span', 'picker-status', item.status);
      status.title = item.status;
      meta.appendChild(status);
      choice.appendChild(meta);
      list.appendChild(choice);
    });
    picker.appendChild(list);

    const footer = document.createElement('footer');
    footer.appendChild(createTextElement(
      'span',
      'selection-count',
      `已选 ${control.selectedIds.size}/${selectableItems.length}`,
    ));
    const confirm = createTextElement(
      'button',
      'confirm',
      `下载已选（${control.selectedIds.size}）`,
    );
    confirm.type = 'button';
    confirm.disabled = control.selectedIds.size === 0;
    confirm.addEventListener('click', async () => {
      const selectedItems = group.items.filter((candidate) => control.selectedIds.has(candidate.id));
      control.open = false;
      renderInlineControl(control, group);
      await startBestDownloads(selectedItems);
    });
    footer.appendChild(confirm);
    picker.appendChild(footer);
    return picker;
  }

  function renderInlineControl(control, group) {
    const state = inlineGroupState(group.items);
    const multiple = group.items.length > 1;
    if (!multiple) {
      control.open = false;
    }

    const style = document.createElement('style');
    style.textContent = `
      :host { all: initial; }
      *, *::before, *::after { box-sizing: border-box; }
      .trigger { position: relative; display: grid; width: 34px; height: 34px; place-items: center; border: 0; border-radius: 999px; padding: 0; color: rgb(83,100,113); background: transparent; cursor: pointer; }
      .trigger:hover:not(:disabled), .trigger[aria-expanded="true"] { color: rgb(29,155,240); background: rgba(29,155,240,.1); }
      .trigger:disabled { cursor: default; }
      .trigger.completed { color: rgb(0,186,124); }
      .trigger.error, .trigger.retry { color: rgb(244,33,46); }
      .trigger svg { width: 18.75px; height: 18.75px; fill: currentColor; }
      .spinner { box-sizing: border-box; width: 17px; height: 17px; border: 2px solid rgba(83,100,113,.35); border-top-color: rgb(29,155,240); border-radius: 50%; animation: spin .8s linear infinite; }
      .badge { position: absolute; right: -2px; top: 0; min-width: 13px; height: 13px; padding: 0 2px; border-radius: 999px; color: white; background: rgb(29,155,240); font: 9px/13px system-ui,-apple-system,sans-serif; text-align: center; }
      @keyframes spin { to { transform: rotate(360deg); } }
    `;
    const download = document.createElement('button');
    download.className = `trigger ${state.kind}`;
    download.type = 'button';
    download.disabled = !multiple && state.disabled;
    download.title = state.title;
    download.setAttribute('aria-label', state.title);
    download.setAttribute('aria-expanded', String(multiple && control.open));
    if (multiple) {
      download.setAttribute('aria-haspopup', 'dialog');
    }
    download.appendChild(createInlineIcon(state.kind));
    if (multiple) {
      download.appendChild(createTextElement('span', 'badge', String(group.items.length)));
    }
    download.addEventListener('click', () => {
      if (multiple) {
        toggleInlinePicker(control, group, state);
      } else {
        startBestDownloads(group.items);
      }
    });
    control.root.replaceChildren(style, download);
    if (multiple && control.open) {
      renderFloatingPicker(control, group, state);
    } else {
      removeFloatingPicker(control);
    }
  }

  function renderInlineControls(items) {
    if (displayMode !== 'inline') {
      clearInlineControls();
      return;
    }
    const placements = new Map();
    for (const group of trayCore.groupCandidates(items)) {
      const actionGroup = findPostActionGroup(group.items);
      if (!actionGroup) {
        continue;
      }
      placements.set(group.key, { group, actionGroup });
    }
    for (const groupKey of [...inlineControls.keys()]) {
      if (!placements.has(groupKey)) {
        removeInlineControl(groupKey);
      }
    }
    for (const placement of placements.values()) {
      inlineControl(placement.group, placement.actionGroup);
    }
  }

  function scheduleInlineRender() {
    if (displayMode !== 'inline' || inlineRenderTimer) {
      return;
    }
    inlineRenderTimer = setTimeout(() => {
      inlineRenderTimer = null;
      renderInlineControls(candidatesForCurrentPage());
    }, 250);
  }

  function sizeTrayForFiveCards(preservedScrollTop) {
    requestAnimationFrame(() => {
      if (!cardContainer || collapsed) {
        return;
      }
      const scrollTop = Number.isFinite(preservedScrollTop)
        ? preservedScrollTop
        : cardContainer.scrollTop;
      const cards = [...cardContainer.querySelectorAll('.card')];
      const availableHeight = Math.max(180, window.innerHeight - 92);
      if (cards.length <= 5) {
        cardContainer.style.maxHeight = `${availableHeight}px`;
        cardContainer.scrollTop = scrollTop;
        return;
      }
      const containerRect = cardContainer.getBoundingClientRect();
      const fifthCardRect = cards[4].getBoundingClientRect();
      const fiveCardHeight = Math.ceil(
        fifthCardRect.bottom - containerRect.top + cardContainer.scrollTop + 8,
      );
      cardContainer.style.maxHeight = `${Math.min(availableHeight, fiveCardHeight)}px`;
      cardContainer.scrollTop = scrollTop;
    });
  }

  function renderTray() {
    ensureTray();
    const visibleCandidates = candidatesForCurrentPage();
    const previousScrollTop = cardContainer.scrollTop;
    const tray = trayRoot.querySelector('.tray');
    tray.classList.toggle('inline-mode', displayMode === 'inline');
    tray.classList.toggle('collapsed', displayMode === 'tray' && collapsed);
    trayRoot.querySelector('.toggle').textContent = collapsed ? '+' : '−';
    for (const modeButton of trayRoot.querySelectorAll('.mode-button')) {
      modeButton.classList.toggle('active', modeButton.dataset.mode === displayMode);
    }
    trayRoot.querySelector('.count').textContent = String(visibleCandidates.length);
    const helperState = trayRoot.querySelector('.helper-state');
    helperState.className = `helper-state ${helperConnected === true ? 'ready' : helperConnected === false ? 'offline' : ''}`;
    helperState.title = helperConnected === true
      ? 'Helper 已连接'
      : helperConnected === false ? `Helper 未连接：${helperConnectionError}` : 'Helper 状态未知';
    cardContainer.replaceChildren();
    if (displayMode === 'inline') {
      renderInlineControls(visibleCandidates);
      return;
    }
    clearInlineControls();
    const note = createTextElement(
      'div',
      `detection-note${helperConnected === false ? ' problem' : ''}`,
      helperConnected === false
        ? `Helper 未连接：${helperConnectionError || '请启动 Helper 后重试'}`
        : `已检测 ${visibleCandidates.length} 个视频；多视频帖子请逐个切换并播放。`,
    );
    cardContainer.appendChild(note);
    if (visibleCandidates.length === 0) {
      cardContainer.appendChild(createTextElement('div', 'empty', '播放视频后，这里会显示可下载内容'));
      sizeTrayForFiveCards(0);
      return;
    }

    for (const group of trayCore.groupCandidates(visibleCandidates)) {
      const groupElement = document.createElement('section');
      groupElement.className = 'post-group';
      const groupHeader = createTextElement('div', 'group-header', '');
      groupHeader.appendChild(createTextElement('span', 'group-title', group.label));
      groupHeader.appendChild(createTextElement('span', 'group-count', `${group.items.length} 个视频`));
      groupElement.appendChild(groupHeader);

      for (const candidate of group.items) {
        const card = document.createElement('section');
        card.className = 'card';
        card.dataset.candidateId = candidate.id;
        const thumbnailUrl = candidate.context?.thumbnailUrl;
        if (thumbnailUrl) {
          const image = document.createElement('img');
          image.className = 'thumb';
          image.src = thumbnailUrl;
          image.alt = '';
          card.appendChild(image);
        } else {
          card.appendChild(createTextElement('div', 'thumb placeholder', 'VIDEO'));
        }

        const meta = document.createElement('div');
        meta.className = 'meta';
        const author = candidate.context?.author ? `@${candidate.context.author}` : '未关联帖子';
        const cardTitle = candidate.context?.postId
          ? `视频 ${candidate.context?.mediaIndex || 1}`
          : `${author} · 视频 ${candidate.context?.mediaIndex || 1}`;
        meta.appendChild(createTextElement('div', 'title', cardTitle));
        meta.appendChild(createTextElement('div', 'detail', `ID ${candidate.mediaId}`));

        const select = document.createElement('select');
        select.className = 'variant';
        for (const variant of candidate.variants || []) {
          const option = document.createElement('option');
          option.value = variant.id;
          const audio = variant.audio?.bitrate ? ` + ${Math.round(variant.audio.bitrate / 1000)}k 音频` : '';
          option.textContent = `${variant.width}×${variant.height}${audio}`;
          select.appendChild(option);
        }
        const selectedVariantId = selectedVariants.get(candidate.id) || candidate.variants?.[0]?.id || '';
        select.value = selectedVariantId;
        selectedVariants.set(candidate.id, select.value);
        meta.appendChild(select);

        const job = latestJob(candidate.id, select.value);
        const statusText = candidate.uiError || candidate.registrationError || jobStatusText(job);
        meta.appendChild(createTextElement('div', 'status', statusText));
        const actions = document.createElement('div');
        actions.className = 'actions';
        const action = downloadButtonState(candidate, job, select.value);
        const pendingRegistration = Boolean(candidate.registrationError);
        const download = createTextElement(
          'button',
          pendingRegistration ? 'action retry-registration' : 'action download',
          pendingRegistration ? '重试连接' : action.label,
        );
        download.type = 'button';
        download.disabled = pendingRegistration ? false : action.disabled;
        actions.appendChild(download);
        if (job && ['queued', 'downloading'].includes(job.status)) {
          const cancel = createTextElement('button', 'action cancel', '取消');
          cancel.type = 'button';
          cancel.dataset.jobId = job.id;
          actions.appendChild(cancel);
        }
        meta.appendChild(actions);
        card.appendChild(meta);
        groupElement.appendChild(card);
      }
      cardContainer.appendChild(groupElement);
    }
    sizeTrayForFiveCards(previousScrollTop);
  }

  function latestJob(candidateId, variantId) {
    return [...jobs.values()]
      .filter((job) => job.candidateId === candidateId && job.variantId === variantId)
      .sort((left, right) => Date.parse(right.createdAt) - Date.parse(left.createdAt))[0] || null;
  }

  function pruneContentState() {
    if (knownCaptures.size > MAX_KNOWN_CAPTURES) {
      for (const mediaId of knownCaptures.keys()) {
        if (knownCaptures.size <= MAX_KNOWN_CAPTURES) break;
        knownCaptures.delete(mediaId);
        contextSignatures.delete(mediaId);
      }
    }

    if (candidates.size > MAX_CANDIDATES) {
      const protectedCandidates = new Set(
        [...jobs.values()]
          .filter((job) => ['queued', 'downloading'].includes(job.status))
          .map((job) => job.candidateId),
      );
      const removable = [...candidates.values()]
        .filter((candidate) => !protectedCandidates.has(candidate.id))
        .sort((left, right) => (Date.parse(left.discoveredAt || '') || 0) - (Date.parse(right.discoveredAt || '') || 0));
      for (const candidate of removable) {
        if (candidates.size <= MAX_CANDIDATES) break;
        candidates.delete(candidate.id);
      }
    }

    if (jobs.size > MAX_JOBS) {
      const removable = [...jobs.values()]
        .filter((job) => ['completed', 'failed', 'cancelled'].includes(job.status))
        .sort((left, right) => Date.parse(left.createdAt || '') - Date.parse(right.createdAt || ''));
      for (const job of removable) {
        if (jobs.size <= MAX_JOBS) break;
        jobs.delete(job.id);
      }
    }
  }

  function downloadButtonState(candidate, job, variantId) {
    let label = '下载';
    if (job?.status === 'completed') {
      label = '已下载';
    } else if (job && ['failed', 'cancelled'].includes(job.status)) {
      label = '重试';
    }
    return {
      label,
      disabled: Boolean(candidate.registrationError)
        || !variantId
        || Boolean(job && ['queued', 'downloading', 'completed'].includes(job.status)),
    };
  }

  function jobStatusText(job) {
    if (!job) {
      return '可以下载';
    }
    switch (job.status) {
      case 'queued': return '等待下载';
      case 'downloading': return `下载中 ${job.progress?.outTimeSeconds?.toFixed?.(1) || 0}s · ${job.progress?.speed || ''}`;
      case 'completed': return `已完成 · ${job.outputPath?.split('/').pop() || ''}`;
      case 'failed': return `失败 · ${localizedJobError(job.error)}`;
      case 'cancelled': return '已取消';
      default: return job.status;
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

  async function startDownload(candidateId, variantId) {
    const candidate = candidates.get(candidateId);
    if (candidate) {
      delete candidate.uiError;
    }
    try {
      const job = await sendMessage({
        type: 'job-create',
        candidateId,
        variantId,
      });
      jobs.set(job.id, job);
      pruneContentState();
      scheduleJobPoll();
    } catch (error) {
      if (candidate) {
        candidate.uiError = error.message;
      }
    }
    renderTray();
  }

  async function retryRegistration(mediaId) {
    const capture = knownCaptures.get(mediaId);
    if (!capture) {
      return;
    }
    const context = extractContext(mediaId);
    try {
      const result = await sendMessage({
        type: 'media-context-updated',
        masterUrl: capture.masterUrl,
        context,
      });
      if (result?.candidate) {
        contextSignatures.set(mediaId, contextSignature(context));
        upsertCandidate(result.candidate);
      }
    } catch (error) {
      upsertPendingCandidate(capture, context, error.message);
    }
  }

  async function startBestDownloads(items) {
    let hasActiveJobs = false;
    for (const candidate of items) {
      const variant = bestVariant(candidate);
      if (candidate.registrationError || !variant?.id) {
        continue;
      }
      const currentJob = latestJob(candidate.id, variant.id);
      if (currentJob && ['queued', 'downloading', 'completed'].includes(currentJob.status)) {
        hasActiveJobs ||= ['queued', 'downloading'].includes(currentJob.status);
        continue;
      }
      delete candidate.uiError;
      try {
        const job = await sendMessage({
          type: 'job-create',
          candidateId: candidate.id,
          variantId: variant.id,
        });
        jobs.set(job.id, job);
        pruneContentState();
        hasActiveJobs ||= ['queued', 'downloading'].includes(job.status);
      } catch (error) {
        candidate.uiError = error.message;
      }
    }
    if (hasActiveJobs) {
      scheduleJobPoll();
    }
    renderTray();
  }

  async function cancelDownload(jobId, candidateId) {
    try {
      const job = await sendMessage({ type: 'job-cancel', jobId });
      jobs.set(job.id, job);
      pruneContentState();
    } catch (error) {
      const candidate = candidates.get(candidateId);
      if (candidate) {
        candidate.uiError = error.message;
      }
    }
    renderTray();
  }

  async function handleTrayClick(event) {
    const modeButton = event.target.closest?.('.mode-button');
    if (modeButton) {
      setDisplayMode(modeButton.dataset.mode);
      return;
    }
    const card = event.target.closest?.('.card');
    if (!card) {
      return;
    }
    if (event.target.classList.contains('download')) {
      const variantId = card.querySelector('.variant')?.value || '';
      await startDownload(card.dataset.candidateId, variantId);
    } else if (event.target.classList.contains('retry-registration')) {
      const candidate = candidates.get(card.dataset.candidateId);
      await retryRegistration(candidate?.mediaId || '');
    } else if (event.target.classList.contains('cancel')) {
      await cancelDownload(event.target.dataset.jobId, card.dataset.candidateId);
    }
  }

  function handleTrayChange(event) {
    if (!event.target.classList.contains('variant')) {
      return;
    }
    const card = event.target.closest('.card');
    if (!card) {
      return;
    }
    selectedVariants.set(card.dataset.candidateId, event.target.value);
    const candidate = candidates.get(card.dataset.candidateId);
    if (candidate) {
      delete candidate.uiError;
    }
    renderTray();
  }

  function notificationDetail(job) {
    if (job.status === 'completed') {
      return job.outputPath?.split(/[\\/]/).pop() || `视频 ${job.mediaId || ''}`;
    }
    return localizedJobError(job.error);
  }

  async function notifyJobTransition(previous, next) {
    if (!previous || previous.status === next.status || !['completed', 'failed'].includes(next.status)) {
      return;
    }
    const stored = await chrome.storage.local.get('downloadNotifications');
    if (stored.downloadNotifications === false) {
      return;
    }
    await sendMessage({
      type: 'job-notification',
      jobId: next.id,
      status: next.status,
      detail: notificationDetail(next),
    });
  }

  function syncJobs(items) {
    const incomingIds = new Set();
    for (const job of items) {
      incomingIds.add(job.id);
      const previous = jobs.get(job.id);
      jobs.set(job.id, job);
      notifyJobTransition(previous, job).catch(() => {});
    }
    for (const [id, job] of jobs) {
      if (incomingIds.has(id) || !['queued', 'downloading'].includes(job.status)) {
        continue;
      }
      jobs.set(id, {
        ...job,
        status: 'failed',
        error: 'Helper 重启后未找到该任务，可以重新下载',
        finishedAt: new Date().toISOString(),
      });
    }
    pruneContentState();
  }

  function scheduleJobPoll() {
    if (jobPollTimer) {
      return;
    }
    jobPollTimer = setTimeout(async () => {
      jobPollTimer = null;
      let hasActive = [...jobs.values()].some((job) => ['queued', 'downloading'].includes(job.status));
      try {
        const items = await sendMessage({ type: 'job-list' });
        syncJobs(items);
        hasActive = items.some((job) => ['queued', 'downloading'].includes(job.status));
      } catch {
        // Keep retrying while a locally known task may still be active.
      }
      renderTray();
      if (hasActive) {
        scheduleJobPoll();
      }
    }, 1000);
  }

  function upsertCandidate(candidate) {
    if (!candidate?.id) {
      return;
    }
    if (candidate.mediaId) {
      candidates.delete(`pending-${candidate.mediaId}`);
    }
    candidates.set(candidate.id, candidate);
    pruneContentState();
    renderTray();
  }

  function upsertPendingCandidate(capture, context, errorMessage) {
    if (!capture?.mediaId) {
      return;
    }
    const id = `pending-${capture.mediaId}`;
    candidates.set(id, {
      id,
      mediaId: capture.mediaId,
      context,
      variants: (capture.variants || []).map((variant, index) => ({
        ...variant,
        id: `pending-${index}`,
      })),
      registrationError: `helper 未就绪：${errorMessage}`,
      discoveredAt: new Date().toISOString(),
    });
    pruneContentState();
    renderTray();
  }

  document.addEventListener('click', () => closeInlinePickers());

  document.addEventListener(EVENT_NAME, async (event) => {
    if (typeof event.detail !== 'string' || event.detail.length > 256_000) {
      return;
    }
    try {
      const capture = JSON.parse(event.detail);
      const url = new URL(capture.masterUrl);
      if (url.protocol !== 'https:' || url.hostname !== 'video.twimg.com') {
        return;
      }
      const mediaMatch = /\/(?:amplify_video|ext_tw_video)\/(\d+)(?:\/|$)/.exec(url.pathname);
      if (!mediaMatch) {
        return;
      }
      capture.mediaId = mediaMatch[1];
      knownCaptures.set(capture.mediaId, capture);
      pruneContentState();
      const context = extractContext(capture.mediaId);
      const result = await sendMessage({
        type: 'hls-master-captured',
        capture,
        context,
      });
      if (result?.candidate) {
        contextSignatures.set(capture.mediaId, contextSignature(context));
        upsertCandidate(result.candidate);
      } else if (result?.candidateError) {
        upsertPendingCandidate(capture, context, result.candidateError);
        scheduleContextScan(5000);
      }
      scheduleContextScan();
    } catch (error) {
      try {
        const capture = JSON.parse(event.detail);
        upsertPendingCandidate(capture, extractContext(capture.mediaId), error.message);
        scheduleContextScan(5000);
      } catch {
        // Ignore malformed events.
      }
    }
  }, true);

  chrome.runtime.onMessage.addListener((message) => {
    if (message?.type === 'candidate-updated') {
      upsertCandidate(message.candidate);
    }
  });

  const observer = new MutationObserver(() => {
    syncCurrentPage();
    scheduleContextScan();
    scheduleInlineRender();
  });
  observer.observe(document, { childList: true, subtree: true });
  window.addEventListener('popstate', syncCurrentPage);
  window.addEventListener('resize', () => {
    sizeTrayForFiveCards();
    scheduleFloatingPickerPosition();
  });
  window.addEventListener('scroll', scheduleFloatingPickerPosition, true);
  window.addEventListener('pagehide', clearInlineControls);
  setInterval(syncCurrentPage, 1000);

  chrome.storage.local.get(DISPLAY_MODE_KEY)
    .then((stored) => {
      if (['tray', 'inline'].includes(stored[DISPLAY_MODE_KEY])) {
        displayMode = stored[DISPLAY_MODE_KEY];
      }
      renderTray();
    })
    .catch(() => {});
  sendMessage({ type: 'job-list' })
    .then((items) => {
      syncJobs(items);
      renderTray();
      if (items.some((job) => ['queued', 'downloading'].includes(job.status))) {
        scheduleJobPoll();
      }
    })
    .catch(() => {
      renderTray();
      scheduleContextScan(5000);
    });
})();
