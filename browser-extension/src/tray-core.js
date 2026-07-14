(function exposeTrayCore(root, factory) {
  'use strict';

  const api = factory();

  if (typeof module === 'object' && module.exports) {
    module.exports = api;
  }

  if (root) {
    Object.defineProperty(root, 'XDownloaderTray', {
      configurable: true,
      value: api,
    });
  }
})(typeof globalThis !== 'undefined' ? globalThis : this, function createTrayCore() {
  'use strict';

  function pageKey(value, baseUrl = 'https://x.com/') {
    if (!value) {
      return '';
    }
    try {
      const url = new URL(value, baseUrl);
      const hostname = (url.hostname === 'twitter.com' || url.hostname.endsWith('.twitter.com'))
        ? 'x.com'
        : url.hostname;
      const pathname = url.pathname.replace(/\/+$/, '') || '/';
      return `${hostname}${pathname}${url.search}`;
    } catch {
      return '';
    }
  }

  function filterCandidatesForPage(candidates, currentUrl) {
    const currentPageKey = pageKey(currentUrl, currentUrl);
    return [...candidates]
      .filter((candidate) => pageKey(candidate.context?.pageUrl, currentUrl) === currentPageKey)
      .sort((left, right) => {
        const leftTime = Date.parse(left.discoveredAt || '') || 0;
        const rightTime = Date.parse(right.discoveredAt || '') || 0;
        return rightTime - leftTime;
      });
  }

  function isPostActionGroup(group) {
    return Boolean(
      group?.querySelector?.('[data-testid="reply"]')
      && group.querySelector('[data-testid="like"]')
      && (group.querySelector('[data-testid="bookmark"]')
        || group.querySelector('button[aria-label*="Share"]')),
    );
  }

  function groupCandidates(items) {
    const groups = new Map();
    for (const candidate of items) {
      const context = candidate.context || {};
      const key = context.postId ? `post-${context.postId}` : `unlinked-${candidate.id}`;
      if (!groups.has(key)) {
        const author = context.author ? `@${context.author}` : '未关联帖子';
        groups.set(key, {
          key,
          label: context.postId ? `${author} · 帖子 ${context.postId}` : author,
          items: [],
        });
      }
      groups.get(key).items.push(candidate);
    }
    for (const group of groups.values()) {
      group.items.sort((left, right) => (
        (left.context?.mediaIndex || 999) - (right.context?.mediaIndex || 999)
      ));
    }
    return [...groups.values()];
  }

  function inlineDownloadState(candidate, job) {
    const variant = candidate?.variants?.[0] || null;
    const dimensions = variant?.width && variant?.height
      ? `${variant.width}×${variant.height}`
      : '最高画质';

    if (candidate?.registrationError) {
      return {
        kind: 'unavailable',
        selectable: false,
        status: candidate.registrationError,
        variantId: '',
      };
    }
    if (!variant?.id) {
      return {
        kind: 'unavailable',
        selectable: false,
        status: '没有可用的最高画质',
        variantId: '',
      };
    }

    if (job?.status === 'queued') {
      return { kind: 'active', selectable: false, status: '等待下载', variantId: variant.id };
    }
    if (job?.status === 'downloading') {
      return { kind: 'active', selectable: false, status: '正在下载', variantId: variant.id };
    }
    if (job?.status === 'completed') {
      return { kind: 'completed', selectable: false, status: '已下载', variantId: variant.id };
    }
    if (job && ['failed', 'cancelled'].includes(job.status)) {
      return {
        kind: 'retry',
        selectable: true,
        status: job.status === 'cancelled' ? '已取消，可重试' : `失败，可重试：${job.error || '未知错误'}`,
        variantId: variant.id,
      };
    }
    if (candidate?.uiError) {
      return {
        kind: 'retry',
        selectable: true,
        status: `可重试：${candidate.uiError}`,
        variantId: variant.id,
      };
    }
    return {
      kind: 'ready',
      selectable: true,
      status: `最高画质 · ${dimensions}`,
      variantId: variant.id,
    };
  }

  return Object.freeze({
    filterCandidatesForPage,
    groupCandidates,
    inlineDownloadState,
    isPostActionGroup,
    pageKey,
  });
});
