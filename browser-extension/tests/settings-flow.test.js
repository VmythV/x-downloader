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
    alarms: {
      create: async () => {},
      clear: async () => {},
      onAlarm: { addListener: () => {} },
    },
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
      session: {
        async get() { return {}; },
        async set() {},
      },
    },
    tabs: {
      async get() { return { url: 'https://x.com/example/status/1' }; },
      sendMessage() { return Promise.resolve(); },
    },
    webRequest: {
      onBeforeRequest: { addListener: () => {} },
    },
  };
  const source = fs.readFileSync(path.join(extensionRoot, 'src/background.js'), 'utf8');
  vm.runInNewContext(source, {
    URL,
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
    disabled: false,
    hidden: false,
    listeners,
    textContent: '',
    value: '',
    addEventListener(type, listener) {
      listeners[type] = listener;
    },
  };
}

test('保存并测试会校验 token 后持久化 helper 设置', async () => {
  const requests = [];
  const harness = createBackgroundHarness(async (url, options) => {
    requests.push({ url, options });
    if (url.endsWith('/v1/health')) {
      return jsonResponse(200, { status: 'ok', version: 'test' });
    }
    if (url.endsWith('/v1/candidates')) {
      return jsonResponse(200, []);
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
  assert.equal(requests.length, 2);
  assert.equal(requests[1].options.headers.Authorization, `Bearer ${token}`);
  assert.deepEqual(JSON.parse(JSON.stringify(harness.local.helperSettings)), {
    baseUrl: 'http://127.0.0.1:17890',
    token,
  });
});

test('配对令牌校验失败时不保存设置', async () => {
  const harness = createBackgroundHarness(async (url) => {
    if (url.endsWith('/v1/health')) {
      return jsonResponse(200, { status: 'ok', version: 'test' });
    }
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
        if (message.type === 'diagnostic-status') {
          return { ok: true, result: null };
        }
        if (message.type === 'helper-settings-test-and-save') {
          return { ok: true, result: { status: 'ok', version: 'test' } };
        }
        throw new Error(`unexpected message: ${message.type}`);
      },
    },
    tabs: { query: async () => [] },
  };
  const source = fs.readFileSync(path.join(extensionRoot, 'popup/popup.js'), 'utf8');
  vm.runInNewContext(source, { chrome, clearTimeout, document, setTimeout });
  await new Promise((resolve) => setImmediate(resolve));

  elements.get('#helper-url').value = 'http://127.0.0.1:17890';
  elements.get('#helper-token').value = 'c'.repeat(32);
  await elements.get('#test-helper').listeners.click();

  assert.deepEqual(JSON.parse(JSON.stringify(messages.at(-1))), {
    type: 'helper-settings-test-and-save',
    baseUrl: 'http://127.0.0.1:17890',
    token: 'c'.repeat(32),
  });
  assert.equal(elements.get('#connection-status').textContent, '已保存 · 正常 · test');
});
