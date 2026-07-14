const test = require('node:test');
const assert = require('node:assert/strict');

const tray = require('../src/tray-core.js');

test('filters candidates to the current X route', () => {
  const items = [
    { id: 'home', context: { pageUrl: 'https://x.com/home' }, discoveredAt: '2026-07-13T01:00:00Z' },
    { id: 'post', context: { pageUrl: 'https://x.com/user/status/123' }, discoveredAt: '2026-07-13T02:00:00Z' },
    { id: 'other', context: { pageUrl: 'https://x.com/user/status/456' }, discoveredAt: '2026-07-13T03:00:00Z' },
  ];

  assert.deepEqual(
    tray.filterCandidatesForPage(items, 'https://x.com/user/status/123').map((item) => item.id),
    ['post'],
  );
});

test('normalizes equivalent Twitter and X routes', () => {
  assert.equal(
    tray.pageKey('https://mobile.twitter.com/user/status/123/'),
    'x.com/user/status/123',
  );
  assert.equal(
    tray.pageKey('https://x.com/user/status/123'),
    'x.com/user/status/123',
  );
});

test('groups media by post and sorts media index', () => {
  const groups = tray.groupCandidates([
    { id: 'two', context: { postId: '123', author: 'user', mediaIndex: 2 } },
    { id: 'one', context: { postId: '123', author: 'user', mediaIndex: 1 } },
    { id: 'other', context: { postId: '456', author: 'other', mediaIndex: 1 } },
  ]);

  assert.equal(groups.length, 2);
  assert.equal(groups[0].label, '@user · 帖子 123');
  assert.deepEqual(groups[0].items.map((item) => item.id), ['one', 'two']);
});

test('recognizes the post action group used for inline download placement', () => {
  const group = (selectors) => ({
    querySelector: (selector) => (selectors.has(selector) ? {} : null),
  });
  const withBookmark = new Set([
    '[data-testid="reply"]',
    '[data-testid="like"]',
    '[data-testid="bookmark"]',
  ]);
  const withShare = new Set([
    '[data-testid="reply"]',
    '[data-testid="like"]',
    'button[aria-label*="Share"]',
  ]);

  assert.equal(tray.isPostActionGroup(group(withBookmark)), true);
  assert.equal(tray.isPostActionGroup(group(withShare)), true);
  assert.equal(tray.isPostActionGroup(group(new Set(['[data-testid="reply"]']))), false);
});

test('describes which inline videos can be selected for download', () => {
  const candidate = {
    id: 'video-one',
    variants: [{ id: 'highest', width: 1920, height: 1080 }],
  };

  assert.deepEqual(tray.inlineDownloadState(candidate, null), {
    kind: 'ready',
    selectable: true,
    status: '最高画质 · 1920×1080',
    variantId: 'highest',
  });
  assert.equal(tray.inlineDownloadState(candidate, { status: 'downloading' }).selectable, false);
  assert.equal(tray.inlineDownloadState(candidate, { status: 'completed' }).kind, 'completed');
  assert.equal(tray.inlineDownloadState(candidate, { status: 'failed', error: 'network' }).selectable, true);
  assert.equal(tray.inlineDownloadState({ ...candidate, registrationError: 'helper 未就绪' }).kind, 'unavailable');
});
