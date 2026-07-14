(function installHighestQualityInterceptor() {
  'use strict';

  const EVENT_NAME = 'x-downloader:hls-master';
  const INSTALL_MARKER = '__xDownloaderHighestQualityInstalled';
  const hls = globalThis.XDownloaderHls;

  if (!hls || globalThis[INSTALL_MARKER]) {
    return;
  }
  globalThis[INSTALL_MARKER] = true;

  const xhrState = new WeakMap();
  const observedXhrs = new WeakSet();
  const nativeOpen = XMLHttpRequest.prototype.open;
  const nativeFetch = globalThis.fetch;

  function publishCapture(masterUrl, rewrite) {
    const payload = {
      capturedAt: Date.now(),
      masterUrl,
      mediaId: hls.extractMediaId(masterUrl),
      selected: rewrite.selected,
      variants: rewrite.variants,
      renditions: rewrite.renditions,
    };

    document.dispatchEvent(new CustomEvent(EVENT_NAME, {
      detail: JSON.stringify(payload),
    }));
  }

  function replaceTextResponse(xhr, rewrittenText) {
    const descriptor = {
      configurable: true,
      enumerable: true,
      value: rewrittenText,
    };

    Object.defineProperty(xhr, 'responseText', descriptor);
    Object.defineProperty(xhr, 'response', descriptor);
  }

  function handleReadyStateChange(event) {
    const xhr = event.currentTarget;
    const state = xhrState.get(xhr);
    if (!state || state.processed || xhr.readyState !== XMLHttpRequest.DONE) {
      return;
    }
    state.processed = true;

    if (xhr.responseType && xhr.responseType !== 'text') {
      return;
    }

    try {
      const originalText = xhr.responseText;
      const rewrite = hls.rewriteMasterPlaylist(originalText, state.url);
      if (!rewrite) {
        return;
      }

      publishCapture(state.url, rewrite);
      replaceTextResponse(xhr, rewrite.text);
    } catch (error) {
      console.warn('[X Downloader] Unable to rewrite HLS master playlist.', error);
    }
  }

  XMLHttpRequest.prototype.open = function patchedOpen(method, url) {
    if (Object.prototype.hasOwnProperty.call(this, 'responseText')) {
      delete this.responseText;
    }
    if (Object.prototype.hasOwnProperty.call(this, 'response')) {
      delete this.response;
    }

    const absoluteUrl = (() => {
      try {
        return new URL(String(url), location.href).href;
      } catch {
        return String(url);
      }
    })();

    xhrState.set(this, {
      processed: false,
      url: absoluteUrl,
    });

    if (hls.isTwitterHlsUrl(absoluteUrl) && !observedXhrs.has(this)) {
      observedXhrs.add(this);
      this.addEventListener('readystatechange', handleReadyStateChange);
    }

    return nativeOpen.apply(this, arguments);
  };

  if (typeof nativeFetch === 'function') {
    globalThis.fetch = async function patchedFetch(input) {
      const response = await nativeFetch.apply(this, arguments);
      const requestUrl = (() => {
        try {
          return new URL(typeof input === 'string' || input instanceof URL ? input : input?.url, location.href).href;
        } catch {
          return response.url || '';
        }
      })();
      if (hls.isTwitterHlsUrl(requestUrl) && response.ok) {
        response.clone().text()
          .then((text) => hls.rewriteMasterPlaylist(text, requestUrl))
          .then((rewrite) => {
            if (rewrite) publishCapture(requestUrl, rewrite);
          })
          .catch(() => {});
      }
      return response;
    };
  }
})();
