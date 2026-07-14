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
    setTimeout,
  });

  async function send(message) {
    return new Promise((resolve) => {
      assert.equal(messageListener(message, {}, resolve), true);
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
      return jsonResponse(200, { status: 'ready', version: 'test', apiVersion: '1' });
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
              status: 'ready', version: 'test', apiVersion: '1', candidateCount: 0,
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
