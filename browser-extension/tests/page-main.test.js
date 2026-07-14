'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const extensionRoot = path.join(__dirname, '..');

test('observes master playlists loaded through fetch without replacing the response', async () => {
  const events = [];
  const master = `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1000,RESOLUTION=640x360
https://video.twimg.com/amplify_video/123/pl/avc1/640x360/video.m3u8
`;
  class FakeXHR {
    addEventListener() {}
    open() {}
  }
  FakeXHR.DONE = 4;
  const response = {
    ok: true,
    url: 'https://video.twimg.com/amplify_video/123/pl/master.m3u8',
    clone() {
      return { text: async () => master };
    },
  };
  const context = vm.createContext({
    CustomEvent: class CustomEvent {
      constructor(type, options) { this.type = type; this.detail = options.detail; }
    },
    URL,
    XMLHttpRequest: FakeXHR,
    console,
    document: { dispatchEvent: (event) => events.push(event) },
    fetch: async () => response,
    location: { href: 'https://x.com/home' },
  });
  vm.runInContext(fs.readFileSync(path.join(extensionRoot, 'src/hls-core.js'), 'utf8'), context);
  vm.runInContext(fs.readFileSync(path.join(extensionRoot, 'src/page-main.js'), 'utf8'), context);

  const returned = await context.fetch(response.url);
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(returned, response);
  assert.equal(events.length, 1);
  assert.equal(JSON.parse(events[0].detail).mediaId, '123');
});
