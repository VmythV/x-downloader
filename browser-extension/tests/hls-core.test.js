'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const hls = require('../src/hls-core.js');

const MASTER = `#EXTM3U
#EXT-X-VERSION:6
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="Default",DEFAULT=YES,URI="audio/128000/audio.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=420000,RESOLUTION=640x360,CODECS="avc1.4d401e,mp4a.40.2",AUDIO="audio"
video/640x360/video.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2600000,AVERAGE-BANDWIDTH=2200000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="audio"
video/1920x1080/video.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1200000,RESOLUTION=1280x720,CODECS="avc1.4d401f,mp4a.40.2",AUDIO="audio"
video/1280x720/video.m3u8
`;

test('parses quoted attribute values containing commas', () => {
  const attributes = hls.parseAttributeList(
    'BANDWIDTH=2600000,CODECS="avc1.640028,mp4a.40.2",RESOLUTION=1920x1080',
  );

  assert.equal(attributes.CODECS, 'avc1.640028,mp4a.40.2');
  assert.equal(attributes.RESOLUTION, '1920x1080');
});

test('selects the actual highest-resolution variant', () => {
  const parsed = hls.parseMasterPlaylist(
    MASTER,
    'https://video.twimg.com/amplify_video/2076268346560196608/pl/master.m3u8',
  );
  const best = hls.selectBestVariant(parsed.variants);

  assert.equal(best.width, 1920);
  assert.equal(best.height, 1080);
  assert.equal(best.averageBandwidth, 2200000);
});

test('rewrites a master playlist while retaining its audio rendition', () => {
  const result = hls.rewriteMasterPlaylist(
    MASTER,
    'https://video.twimg.com/amplify_video/2076268346560196608/pl/master.m3u8',
  );

  assert.match(result.text, /#EXT-X-MEDIA:TYPE=AUDIO/);
  assert.match(result.text, /video\/1920x1080\/video\.m3u8/);
  assert.doesNotMatch(result.text, /video\/1280x720\/video\.m3u8/);
  assert.doesNotMatch(result.text, /video\/640x360\/video\.m3u8/);
  assert.equal(result.variants.length, 3);
  assert.equal(result.renditions[0].uri, 'https://video.twimg.com/amplify_video/2076268346560196608/pl/audio/128000/audio.m3u8');
});

test('does not treat a media playlist as a master playlist', () => {
  const media = '#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4.0,\nsegment.m4s\n';
  assert.equal(hls.rewriteMasterPlaylist(media, 'https://video.twimg.com/video.m3u8'), null);
});

test('extracts the media identifier from Twitter CDN URLs', () => {
  assert.equal(
    hls.extractMediaId('https://video.twimg.com/amplify_video/2076268346560196608/pl/avc1/1280x720/video.m3u8'),
    '2076268346560196608',
  );
});

