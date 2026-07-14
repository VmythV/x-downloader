(function exposeHlsCore(root, factory) {
  'use strict';

  const api = factory();

  if (typeof module === 'object' && module.exports) {
    module.exports = api;
  }

  if (root) {
    Object.defineProperty(root, 'XDownloaderHls', {
      configurable: true,
      value: api,
    });
  }
})(typeof globalThis !== 'undefined' ? globalThis : this, function createHlsCore() {
  'use strict';

  const STREAM_TAG = '#EXT-X-STREAM-INF:';
  const MEDIA_TAG = '#EXT-X-MEDIA:';

  function parseAttributeList(source) {
    const attributes = {};
    const matcher = /([A-Z0-9-]+)=("(?:[^"\\]|\\.)*"|[^,]*)/gi;
    let match;

    while ((match = matcher.exec(source)) !== null) {
      const key = match[1].toUpperCase();
      const rawValue = match[2];
      attributes[key] = rawValue.startsWith('"')
        ? rawValue.slice(1, -1).replace(/\\"/g, '"')
        : rawValue;
    }

    return attributes;
  }

  function parsePositiveInteger(value) {
    const number = Number.parseInt(value, 10);
    return Number.isFinite(number) && number > 0 ? number : 0;
  }

  function parseResolution(value) {
    const match = /^(\d+)x(\d+)$/i.exec(value || '');
    if (!match) {
      return { width: 0, height: 0 };
    }

    return {
      width: parsePositiveInteger(match[1]),
      height: parsePositiveInteger(match[2]),
    };
  }

  function resolveUrl(uri, baseUrl) {
    try {
      return new URL(uri, baseUrl).href;
    } catch {
      return uri;
    }
  }

  function parseMasterPlaylist(text, baseUrl) {
    if (typeof text !== 'string' || !text.includes(STREAM_TAG)) {
      return null;
    }

    const normalized = text.replace(/\r\n/g, '\n');
    const lines = normalized.split('\n');
    const variants = [];
    const renditions = [];

    for (let index = 0; index < lines.length; index += 1) {
      const line = lines[index].trim();

      if (line.startsWith(MEDIA_TAG)) {
        const attributes = parseAttributeList(line.slice(MEDIA_TAG.length));
        renditions.push({
          type: attributes.TYPE || '',
          groupId: attributes['GROUP-ID'] || '',
          name: attributes.NAME || '',
          uri: attributes.URI ? resolveUrl(attributes.URI, baseUrl) : '',
          attributes,
          lineIndex: index,
        });
        continue;
      }

      if (!line.startsWith(STREAM_TAG)) {
        continue;
      }

      const attributes = parseAttributeList(line.slice(STREAM_TAG.length));
      let uriIndex = index + 1;
      while (uriIndex < lines.length) {
        const candidate = lines[uriIndex].trim();
        if (candidate && !candidate.startsWith('#')) {
          break;
        }
        uriIndex += 1;
      }

      if (uriIndex >= lines.length) {
        continue;
      }

      const resolution = parseResolution(attributes.RESOLUTION);
      variants.push({
        index: variants.length,
        tagIndex: index,
        uriIndex,
        uri: resolveUrl(lines[uriIndex].trim(), baseUrl),
        width: resolution.width,
        height: resolution.height,
        bandwidth: parsePositiveInteger(attributes.BANDWIDTH),
        averageBandwidth: parsePositiveInteger(attributes['AVERAGE-BANDWIDTH']),
        codecs: attributes.CODECS || '',
        audioGroup: attributes.AUDIO || '',
        attributes,
      });

      index = uriIndex;
    }

    if (variants.length === 0) {
      return null;
    }

    return {
      lines,
      variants,
      renditions,
      lineEnding: text.includes('\r\n') ? '\r\n' : '\n',
      hasTrailingNewline: /\r?\n$/.test(text),
    };
  }

  function compareVariantQuality(left, right) {
    const leftPixels = left.width * left.height;
    const rightPixels = right.width * right.height;
    if (leftPixels !== rightPixels) {
      return leftPixels - rightPixels;
    }

    const leftShortEdge = Math.min(left.width, left.height);
    const rightShortEdge = Math.min(right.width, right.height);
    if (leftShortEdge !== rightShortEdge) {
      return leftShortEdge - rightShortEdge;
    }

    const leftBandwidth = left.averageBandwidth || left.bandwidth;
    const rightBandwidth = right.averageBandwidth || right.bandwidth;
    return leftBandwidth - rightBandwidth;
  }

  function selectBestVariant(variants) {
    if (!Array.isArray(variants) || variants.length === 0) {
      return null;
    }

    return variants.reduce((best, candidate) => (
      compareVariantQuality(candidate, best) > 0 ? candidate : best
    ));
  }

  function rewriteMasterPlaylist(text, baseUrl) {
    const parsed = parseMasterPlaylist(text, baseUrl);
    if (!parsed) {
      return null;
    }

    const selected = selectBestVariant(parsed.variants);
    const excludedLines = new Set();

    for (const variant of parsed.variants) {
      if (variant !== selected) {
        excludedLines.add(variant.tagIndex);
        excludedLines.add(variant.uriIndex);
      }
    }

    let rewritten = parsed.lines
      .filter((_, index) => !excludedLines.has(index))
      .join(parsed.lineEnding);

    if (parsed.hasTrailingNewline && !rewritten.endsWith(parsed.lineEnding)) {
      rewritten += parsed.lineEnding;
    }

    return {
      text: rewritten,
      selected,
      variants: parsed.variants,
      renditions: parsed.renditions,
    };
  }

  function isTwitterHlsUrl(value) {
    try {
      const url = new URL(String(value), globalThis.location?.href);
      return url.protocol === 'https:'
        && url.hostname === 'video.twimg.com'
        && url.pathname.toLowerCase().endsWith('.m3u8');
    } catch {
      return false;
    }
  }

  function extractMediaId(value) {
    try {
      const url = new URL(String(value), globalThis.location?.href);
      const match = /\/(?:amplify_video|ext_tw_video)\/(\d+)(?:\/|$)/.exec(url.pathname);
      return match ? match[1] : '';
    } catch {
      return '';
    }
  }

  return Object.freeze({
    extractMediaId,
    isTwitterHlsUrl,
    parseAttributeList,
    parseMasterPlaylist,
    rewriteMasterPlaylist,
    selectBestVariant,
  });
});

