'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const extensionRoot = path.join(__dirname, '..');

function jsonResponse(status, payload) {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => payload,
  };
}

function createBackgroundHarness(fetchImpl) {
  let messageListener = null;
  const local = {};
  const setCalls = [];
  const chrome = {
    notifications: { create: async () => 'notification' },
    runtime: {
      onMessage: {
        addListener(listener) {
          messageListener = listener;
        },
      },
    },
    storage: {
      local: {
        async get(key) {
          if (Array.isArray(key)) {
            return Object.fromEntries(key.map((item) => [item, local[item]]));
          }
          return { [key]: local[key] };
        },
        async set(values) {
          Object.assign(local, values);
          setCalls.push(values);
        },
      },
    },
    tabs: {
      async get() { return { url: 'https://x.com/example/status/1' }; },
      sendMessage() { return Promise.resolve(); },
    },
  };
  const source = fs.readFileSync(path.join(extensionRoot, 'src/background.js'), 'utf8');
  vm.runInNewContext(source, {
    URL,
    AbortController,
    chrome,
    clearTimeout,
    fetch: fetchImpl,
    navigator: { userAgent: 'Mozilla/5.0 TestChrome/148.0.0.0' },
    setTimeout,
  });

  async function send(message, sender = {}) {
    return new Promise((resolve) => {
      assert.equal(messageListener(message, sender, resolve), true);
    });
  }

  return { local, send, setCalls };
}

function createElement() {
  const listeners = {};
  return {
    children: [],
    className: '',
    dataset: {},
    disabled: false,
    hidden: false,
    listeners,
    open: false,
    textContent: '',
    value: '',
    addEventListener(type, listener) {
      listeners[type] = listener;
    },
    append(...items) {
      this.children.push(...items);
    },
    appendChild(item) {
      this.children.push(item);
      return item;
    },
    replaceChildren(...items) {
      this.children = items;
    },
    setAttribute() {},
    get childElementCount() {
      return this.children.length;
    },
  };
}

test('保存并测试会校验 token 后持久化 helper 设置', async () => {
  const requests = [];
  const harness = createBackgroundHarness(async (url, options) => {
    requests.push({ url, options });
    if (url.endsWith('/v1/status')) {
      return jsonResponse(200, { status: 'ready', version: 'test', apiVersion: '4' });
    }
    throw new Error(`unexpected URL: ${url}`);
  });
  const token = 'a'.repeat(32);

  const response = await harness.send({
    type: 'helper-settings-test-and-save',
    baseUrl: 'http://127.0.0.1:17890',
    token,
  });

  assert.equal(response.ok, true);
  assert.equal(requests.length, 1);
  assert.equal(requests[0].options.headers.Authorization, `Bearer ${token}`);
  assert.deepEqual(JSON.parse(JSON.stringify(harness.local.helperSettings)), {
    baseUrl: 'http://127.0.0.1:17890',
    token,
  });
});

test('候选注册使用扩展读取的浏览器 User-Agent', async () => {
  const requests = [];
  const harness = createBackgroundHarness(async (url, options) => {
    requests.push({ url, options });
    return jsonResponse(200, { id: 'media-123', mediaId: '123', variants: [] });
  });
  harness.local.helperSettings = { baseUrl: 'http://127.0.0.1:17890', token: 'u'.repeat(32) };

  const response = await harness.send({
    type: 'hls-master-captured',
    capture: { masterUrl: 'https://video.twimg.com/amplify_video/123/pl/master.m3u8' },
    context: { postId: '456' },
  }, { tab: { id: 7, url: 'https://x.com/user/status/456' } });
  await harness.send({ type: 'job-create', candidateId: 'media-123', variantId: 'highest' });

  assert.equal(response.ok, true);
  assert.equal(requests.length, 2);
  const body = JSON.parse(requests[0].options.body);
  assert.equal(body.userAgent, 'Mozilla/5.0 TestChrome/148.0.0.0');
  assert.equal(body.masterUrl, 'https://video.twimg.com/amplify_video/123/pl/master.m3u8');
  assert.equal(JSON.parse(requests[1].options.body).userAgent, 'Mozilla/5.0 TestChrome/148.0.0.0');
});

test('关闭下载总开关后不注册候选或创建任务', async () => {
  let requestCount = 0;
  const harness = createBackgroundHarness(async () => {
    requestCount++;
    return jsonResponse(200, {});
  });
  harness.local.helperSettings = { baseUrl: 'http://127.0.0.1:17890', token: 'v'.repeat(32) };
  harness.local.downloadEnabled = false;

  const capture = await harness.send({
    type: 'hls-master-captured',
    capture: { masterUrl: 'https://video.twimg.com/amplify_video/123/pl/master.m3u8' },
  }, { tab: { id: 7, url: 'https://x.com/home' } });
  const job = await harness.send({ type: 'job-create', candidateId: 'media-123' });

  assert.deepEqual(JSON.parse(JSON.stringify(capture.result)), { candidate: null, disabled: true });
  assert.deepEqual(JSON.parse(JSON.stringify(job)), { ok: false, error: '下载功能已关闭，请在设置中开启' });
  assert.equal(requestCount, 0);
});

test('应用设置通过 Helper API 读取、更新并选择下载目录', async () => {
  const requests = [];
  const harness = createBackgroundHarness(async (url, options) => {
    requests.push({ url, options });
    if (url.endsWith('/v1/settings/pick-download-directory')) {
      return jsonResponse(200, { cancelled: false, downloadDir: '/tmp/picked' });
    }
    if (url.endsWith('/v1/settings') && options.method === 'PUT') {
      return jsonResponse(200, { downloadDir: '/tmp/picked', defaultDownloadDir: '/tmp/default' });
    }
    if (url.endsWith('/v1/settings')) {
      return jsonResponse(200, { downloadDir: '/tmp/current', defaultDownloadDir: '/tmp/default' });
    }
    throw new Error(`unexpected URL: ${url}`);
  });
  harness.local.helperSettings = {
    baseUrl: 'http://127.0.0.1:17890',
    token: 'd'.repeat(32),
  };

  const current = await harness.send({ type: 'app-settings-get' });
  const picked = await harness.send({ type: 'app-settings-pick-download-directory' });
  const updated = await harness.send({
    type: 'app-settings-update', downloadDir: '/tmp/picked',
    filenameTemplate: '{author}_{mediaId}.{ext}', concurrency: 3, retryCount: 2,
  });

  assert.equal(current.result.downloadDir, '/tmp/current');
  assert.equal(picked.result.downloadDir, '/tmp/picked');
  assert.equal(updated.result.downloadDir, '/tmp/picked');
  assert.deepEqual(requests.map((request) => [new URL(request.url).pathname, request.options.method || 'GET']), [
    ['/v1/settings', 'GET'],
    ['/v1/settings/pick-download-directory', 'POST'],
    ['/v1/settings', 'PUT'],
  ]);
  assert.deepEqual(JSON.parse(requests[2].options.body), {
    downloadDir: '/tmp/picked', filenameTemplate: '{author}_{mediaId}.{ext}', concurrency: 3, retryCount: 2,
  });
  assert.equal('timeoutMs' in requests[1].options, false);
});

test('配对令牌校验失败时不保存设置', async () => {
  const harness = createBackgroundHarness(async (url) => {
    return jsonResponse(401, { error: 'invalid bearer token' });
  });

  const response = await harness.send({
    type: 'helper-settings-test-and-save',
    baseUrl: 'http://127.0.0.1:17890',
    token: 'b'.repeat(32),
  });

  assert.deepEqual(JSON.parse(JSON.stringify(response)), { ok: false, error: '配对令牌无效' });
  assert.equal(harness.setCalls.length, 0);
  assert.equal(harness.local.helperSettings, undefined);
});

test('弹窗的测试操作同时提交地址和 token 供保存', async () => {
  const elements = new Map();
  const messages = [];
  const document = {
    createElement,
    querySelector(selector) {
      if (!elements.has(selector)) {
        elements.set(selector, createElement());
      }
      return elements.get(selector);
    },
  };
  const chrome = {
    runtime: {
      async sendMessage(message) {
        messages.push(message);
        if (message.type === 'helper-settings-get') {
          return { ok: true, result: { baseUrl: 'http://127.0.0.1:17890', token: '' } };
        }
        if (message.type === 'helper-settings-test-and-save') {
          return {
            ok: true,
            result: {
        status: 'ready', version: 'test', apiVersion: '4', candidateCount: 0,
              jobs: {},
              readiness: {
                ffmpegReady: true, ffmpegPath: 'ffmpeg', downloadDir: '/tmp',
                downloadDirWritable: true, persistenceEnabled: true, proxyConfigured: false,
              },
            },
          };
        }
        if (message.type === 'job-list') {
          return { ok: true, result: [] };
        }
        throw new Error(`unexpected message: ${message.type}`);
      },
    },
    storage: {
      local: {
        get: async () => ({}),
        set: async () => {},
      },
    },
  };
  const source = fs.readFileSync(path.join(extensionRoot, 'popup/popup.js'), 'utf8');
  vm.runInNewContext(source, { chrome, clearTimeout, document, setTimeout });
  await new Promise((resolve) => setImmediate(resolve));

  elements.get('#helper-url').value = 'http://127.0.0.1:17890';
  elements.get('#helper-token').value = 'c'.repeat(32);
  await elements.get('#test-helper').listeners.click();

  const submitted = messages.find((message) => message.type === 'helper-settings-test-and-save');
  assert.deepEqual(JSON.parse(JSON.stringify(submitted)), {
    type: 'helper-settings-test-and-save',
    baseUrl: 'http://127.0.0.1:17890',
    token: 'c'.repeat(32),
  });
  assert.equal(elements.get('#connection-status').textContent, '已保存');
});

test('设置页选择目录后会保存为 Helper 应用设置', async () => {
  const elements = new Map();
  const messages = [];
  const document = {
    querySelector(selector) {
      if (!elements.has(selector)) elements.set(selector, createElement());
      return elements.get(selector);
    },
  };
  const chrome = {
    runtime: {
      async sendMessage(message) {
        messages.push(message);
        if (message.type === 'helper-settings-get') {
          return { ok: true, result: { baseUrl: 'http://127.0.0.1:17890', token: 'e'.repeat(32) } };
        }
        if (message.type === 'helper-status') {
      return { ok: true, result: { status: 'ready', version: 'test', apiVersion: '4' } };
        }
        if (message.type === 'app-settings-get') {
      return { ok: true, result: {
        downloadDir: '/tmp/current', defaultDownloadDir: '/tmp/default',
        filenameTemplate: '{mediaId}.{ext}', defaultFilenameTemplate: '{mediaId}.{ext}',
        concurrency: 1, defaultConcurrency: 1, retryCount: 1, defaultRetryCount: 1,
      } };
        }
        if (message.type === 'app-settings-pick-download-directory') {
          return { ok: true, result: { cancelled: false, downloadDir: '/tmp/picked' } };
        }
        if (message.type === 'app-settings-update') {
      return { ok: true, result: {
        downloadDir: message.downloadDir || '/tmp/current', defaultDownloadDir: '/tmp/default',
        filenameTemplate: message.filenameTemplate || '{mediaId}.{ext}', defaultFilenameTemplate: '{mediaId}.{ext}',
        concurrency: message.concurrency || 1, defaultConcurrency: 1,
        retryCount: Number.isInteger(message.retryCount) ? message.retryCount : 1, defaultRetryCount: 1,
      } };
        }
        throw new Error(`unexpected message: ${message.type}`);
      },
    },
    storage: { local: { get: async () => ({}), set: async () => {} } },
  };
  const source = fs.readFileSync(path.join(extensionRoot, 'options/options.js'), 'utf8');
  vm.runInNewContext(source, { chrome, document });
  await new Promise((resolve) => setImmediate(resolve));

  await elements.get('#pick-directory').listeners.click();
  assert.equal(elements.get('#download-dir').value, '/tmp/picked');
  await elements.get('#save-directory').listeners.click();

  const update = messages.find((message) => message.type === 'app-settings-update');
  assert.deepEqual(JSON.parse(JSON.stringify(update)), {
    type: 'app-settings-update',
    downloadDir: '/tmp/picked',
  });
  assert.equal(elements.get('#directory-status').textContent, '当前目录已保存，新任务会自动使用');

  elements.get('#filename-template').value = '{author}_{mediaId}.{ext}';
  elements.get('#download-concurrency').value = '3';
  elements.get('#retry-count').value = '2';
  await elements.get('#save-rules').listeners.click();
  const rulesUpdate = messages.find((message) => message.type === 'app-settings-update' && message.filenameTemplate);
  assert.deepEqual(JSON.parse(JSON.stringify(rulesUpdate)), {
    type: 'app-settings-update', filenameTemplate: '{author}_{mediaId}.{ext}', concurrency: 3, retryCount: 2,
  });
});
