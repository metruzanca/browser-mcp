"use strict";

// Browser MCP — background service worker.
//
// Responsibilities:
//   * own a WebSocket connection to the local MCP bridge (ws://127.0.0.1:<port>/ws)
//   * keep the service worker alive with a keepalive ping every 20s (Chrome 116+)
//   * relay bridge requests to the active tab via chrome.scripting.executeScript
//   * serve the popup (status / connect / disconnect)

const DEFAULT_PORT = 18765;
const RECONNECT_DELAY_MS = 3000;
const KEEPALIVE_INTERVAL_MS = 10000;

let ws = null;
let reconnectTimer = null;
let keepAliveTimer = null;
let manualDisconnect = false;
let lastStatus = { connected: false, connecting: false, error: null, url: null };

// ---------------------------------------------------------------------------
// Configuration & status
// ---------------------------------------------------------------------------

async function getConfig() {
  const c = await chrome.storage.local.get(["port", "autoConnect"]);
  return {
    port: c.port || DEFAULT_PORT,
    autoConnect: c.autoConnect !== false,
  };
}

function setStatus(s) {
  lastStatus = Object.assign({}, lastStatus, s);
}

function wsUrl(port) {
  return "ws://127.0.0.1:" + port + "/ws";
}

// ---------------------------------------------------------------------------
// WebSocket lifecycle
// ---------------------------------------------------------------------------

function connect() {
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
    return;
  }
  getConfig().then(({ port }) => {
    const url = wsUrl(port);
    let w;
    try {
      w = new WebSocket(url);
    } catch (e) {
      setStatus({ connected: false, connecting: false, error: String(e), url });
      scheduleReconnect();
      return;
    }
    ws = w;
    manualDisconnect = false;
    setStatus({ connected: false, connecting: true, error: null, url });

    w.onopen = () => {
      setStatus({ connected: true, connecting: false, error: null, url });
      startKeepAlive();
    };

    w.onmessage = (ev) => {
      let msg;
      try {
        msg = JSON.parse(ev.data);
      } catch (e) {
        return;
      }
      if (msg && msg.type === "ping") {
        sendSafe({ type: "pong" });
        return;
      }
      if (msg && msg.id && msg.action) {
        // Per-request timeout: honor the caller's timeoutMs (so busy pages can
        // be given more time), otherwise default to 20s. Reply with an error
        // instead of letting a hung request burn the client's full timeout.
        const reqTimeoutMs = (msg.params && msg.params.timeoutMs) || 20000;
        Promise.race([
          dispatch(msg),
          new Promise((_, reject) =>
            setTimeout(() => reject(new Error("browser: request timed out after " + reqTimeoutMs + "ms")), reqTimeoutMs)
          ),
        ]).then(
          (reply) => sendSafe(Object.assign({ id: msg.id, session: msg.session }, reply)),
          (err) => sendSafe({ id: msg.id, session: msg.session, ok: false, error: (err && err.message) || String(err) })
        );
      }
    };

    w.onclose = (ev) => {
      stopKeepAlive();
      if (ws === w) ws = null;
      setStatus({ connected: false, connecting: false, error: "closed (" + ev.code + ")" });
      if (!manualDisconnect) scheduleReconnect();
    };

    w.onerror = () => {
      try {
        w.close();
      } catch (e) {}
    };
  });
}

function disconnect() {
  manualDisconnect = true;
  stopKeepAlive();
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  if (ws) {
    try {
      ws.close();
    } catch (e) {}
    ws = null;
  }
  setStatus({ connected: false, connecting: false, error: "disconnected" });
}

function sendSafe(obj) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    try {
      ws.send(JSON.stringify(obj));
    } catch (e) {}
  }
}

function startKeepAlive() {
  stopKeepAlive();
  keepAliveTimer = setInterval(() => {
    sendSafe({ type: "ping" });
  }, KEEPALIVE_INTERVAL_MS);
}

function stopKeepAlive() {
  if (keepAliveTimer) {
    clearInterval(keepAliveTimer);
    keepAliveTimer = null;
  }
}

function scheduleReconnect() {
  if (reconnectTimer || manualDisconnect) return;
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    connect();
  }, RECONNECT_DELAY_MS);
}

// ---------------------------------------------------------------------------
// Bridge request dispatch
// ---------------------------------------------------------------------------

async function dispatch(msg) {
  try {
    switch (msg.action) {
      case "getTabInfo":
        return await getTabInfo(msg.params);
      case "captureTab":
        return await captureTab(msg.params);
      case "executeScript":
        return await executeScript(msg.params);
      case "listWindows":
        return await listWindows(msg.params);
      case "listTabs":
        return await listTabs(msg.params);
      default:
        return { ok: false, error: "Unknown action: " + msg.action };
    }
  } catch (e) {
    return { ok: false, error: (e && e.stack) || String(e) };
  }
}

// Resolve which tab an action targets, honoring the session pin injected by
// the hub (pinnedTab/pinnedWindow) plus per-call tabId/windowId. Falls back
// to the active tab of the focused window.
async function resolveTargetTab(params) {
  params = params || {};
  let tabId = params.tabId;
  if (params.pinnedTab) {
    if (tabId && tabId !== params.pinnedTab) {
      throw new Error(
        "This session is pinned to tab " + params.pinnedTab + "; cannot target tab " + tabId + ". Update the pin with browser_set_target."
      );
    }
    tabId = params.pinnedTab;
  }
  if (tabId && params.pinnedWindow) {
    const tab = await chrome.tabs.get(tabId);
    if (tab.windowId !== params.pinnedWindow) {
      throw new Error(
        "This session is pinned to window " + params.pinnedWindow + "; tab " + tabId + " is in window " + tab.windowId + ". Update the pin with browser_set_target."
      );
    }
  }
  if (tabId) return tabId;
  if (params.windowId) {
    const [tab] = await chrome.tabs.query({ windowId: params.windowId, active: true });
    if (!tab) throw new Error("No active tab in window " + params.windowId);
    return tab.id;
  }
  const [tab] = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
  if (!tab) throw new Error("No active tab found");
  return tab.id;
}

async function getTabInfo(params) {
  const tabId = await resolveTargetTab(params);
  const tab = await chrome.tabs.get(tabId);
  return {
    ok: true,
    data: {
      url: tab.url || "",
      title: tab.title || "",
      tabId: tab.id,
      windowId: tab.windowId,
      favIconUrl: tab.favIconUrl || "",
      status: tab.status || "",
    },
  };
}

async function captureTab(params) {
  const tabId = await resolveTargetTab(params);
  const tab = await chrome.tabs.get(tabId);
  const dataUrl = await chrome.tabs.captureVisibleTab(tab.windowId, { format: "png" });
  return { ok: true, data: { dataUrl: dataUrl } };
}

async function listWindows() {
  const wins = await chrome.windows.getAll({ populate: false });
  const out = [];
  for (const w of wins) {
    let active = null;
    try {
      const tabs = await chrome.tabs.query({ windowId: w.id, active: true });
      active = tabs[0]
        ? { tabId: tabs[0].id, url: tabs[0].url || "", title: tabs[0].title || "" }
        : null;
    } catch (e) {}
    out.push({ windowId: w.id, focused: !!w.focused, type: w.type || "", state: w.state || "", activeTab: active });
  }
  return { ok: true, data: { windows: out } };
}

async function listTabs(params) {
  const q = params && params.windowId ? { windowId: params.windowId } : {};
  const tabs = await chrome.tabs.query(q);
  const out = tabs.map((t) => ({
    tabId: t.id,
    windowId: t.windowId,
    url: t.url || "",
    title: t.title || "",
    active: !!t.active,
    pinned: !!t.pinned,
  }));
  return { ok: true, data: { tabs: out } };
}

async function executeScript(params) {
  const tabId = await resolveTargetTab(params);
  const world = (params && params.world) || "main";
  const payload = {
    code: (params && params.code) || "",
    args: (params && params.args) || {},
    timeoutMs: (params && params.timeoutMs) || 60000,
    runtime: BMCP_RUNTIME,
  };

  let out = await runInWorld(tabId, world, payload);
  if (
    !out.ok &&
    world === "main" &&
    /unsafe-eval|EvalError|Refused to evaluate|Content Security Policy|not a function/i.test(out.error || "")
  ) {
    out = await runInWorld(tabId, "isolated", payload);
    if (out.ok) out.worldUsed = "isolated";
  } else if (out.ok) {
    out.worldUsed = world;
  }
  return out;
}

async function runInWorld(tabId, world, payload) {
  try {
    const results = await chrome.scripting.executeScript({
      target: { tabId: tabId },
      world: world === "isolated" ? "ISOLATED" : "MAIN",
      func: RUN_FUNC,
      args: [payload],
    });
    const r = results && results[0] && results[0].result;
    if (!r) {
      return {
        ok: false,
        error:
          "Script returned no result. The page may be unsupported (chrome://, the Chrome Web Store, or a PDF viewer). Navigate to a normal website and try again.",
      };
    }
    return r;
  } catch (e) {
    return { ok: false, error: (e && e.message) || String(e) };
  }
}

// ---------------------------------------------------------------------------
// Popup
// ---------------------------------------------------------------------------

chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  (async () => {
    try {
      if (msg && msg.type === "getStatus") {
        let tab = null;
        try {
          const tabId = await resolveTargetTab(null);
          const t = await chrome.tabs.get(tabId);
          tab = { url: t.url, title: t.title };
        } catch (e) {}
        sendResponse({ status: lastStatus, tab: tab });
      } else if (msg && msg.type === "setConfig") {
        await chrome.storage.local.set({
          port: msg.port || DEFAULT_PORT,
          autoConnect: msg.autoConnect !== false,
        });
        disconnect();
        connect();
        sendResponse({ ok: true });
      } else if (msg && msg.type === "connect") {
        connect();
        sendResponse({ ok: true });
      } else if (msg && msg.type === "disconnect") {
        disconnect();
        sendResponse({ ok: true });
      } else {
        sendResponse({ ok: false, error: "unknown message type" });
      }
    } catch (e) {
      sendResponse({ ok: false, error: String(e) });
    }
  })();
  return true; // keep the message channel open for the async response
});

// ---------------------------------------------------------------------------
// Startup
// ---------------------------------------------------------------------------

chrome.runtime.onInstalled.addListener(() => connect());
chrome.runtime.onStartup.addListener(() => connect());
getConfig().then(({ autoConnect }) => {
  if (autoConnect) connect();
});

// ---------------------------------------------------------------------------
// RUN_FUNC — the fixed function injected into the page.
// Chrome serializes this function (no closures!), so everything it needs comes
// from `payload`. It installs the bmcp runtime if needed, then evals the
// agent's code inside an async function with `args` and `bmcp` in scope.
// ---------------------------------------------------------------------------

const RUN_FUNC = async (payload) => {
  const timeout = payload.timeoutMs || 60000;
  const maxBytes = payload.maxResultBytes || 200000;

  const withTimeout = (p, ms) =>
    new Promise((resolve, reject) => {
      const t = setTimeout(() => reject(new Error("Script timed out after " + ms + "ms")), ms);
      p.then(
        (v) => {
          clearTimeout(t);
          resolve(v);
        },
        (e) => {
          clearTimeout(t);
          reject(e);
        }
      );
    });

  try {
    if (!window.__bmcp) {
      eval(payload.runtime);
    }
    const bmcp = window.__bmcp;
    if (!bmcp) return { ok: false, error: "bmcp runtime failed to initialize" };

    const fn = new Function("args", "bmcp", '"use strict"; return (async () => {\n' + payload.code + "\n})();");
    const result = await withTimeout(fn(payload.args || {}, bmcp), timeout);
    const data = bmcp.serialize(result);
    const json = JSON.stringify(data);
    if (json && json.length > maxBytes) {
      return {
        ok: true,
        truncated: true,
        data: { __truncated: true, size: json.length, head: json.slice(0, 4000) },
      };
    }
    return { ok: true, data: data === undefined ? null : data };
  } catch (e) {
    return { ok: false, error: (e && e.stack) || String(e) };
  }
};

// ---------------------------------------------------------------------------
// BMCP_RUNTIME — the helper library installed on `window.__bmcp` in the
// target world. Avoid backticks and ${...} here (the whole string is a
// template literal below).
// ---------------------------------------------------------------------------

const BMCP_RUNTIME = `
(function () {
  if (window.__bmcp) return;
  var bmcp = {};

  function idFrequency() {
    var freq = {};
    var all = document.querySelectorAll('[id]');
    for (var i = 0; i < all.length; i++) {
      var id = all[i].id;
      if (id) freq[id] = (freq[id] || 0) + 1;
    }
    return freq;
  }

  // Unique CSS path. Short-circuits to #id only when that id is unique on the
  // page; cloned rows (Add Experience, etc.) share ids, so those fall through
  // to a full :nth-of-type path that stays unambiguous per row.
  function cssPath(el, idFreq) {
    if (!el || el.nodeType !== 1) return '';
    if (!idFreq) idFreq = bmcp._idFreq || (bmcp._idFreq = idFrequency());
    var parts = [];
    var node = el;
    while (node && node.nodeType === 1 && node !== document.body && node !== document.documentElement) {
      var seg = node.tagName.toLowerCase();
      if (node.id && !(idFreq[node.id] > 1)) {
        seg = '#' + CSS.escape(node.id);
        parts.unshift(seg);
        break;
      }
      var parent = node.parentElement;
      if (parent) {
        var siblings = Array.prototype.filter.call(parent.children, function (c) { return c.tagName === node.tagName; });
        if (siblings.length > 1) {
          seg += ':nth-of-type(' + (siblings.indexOf(node) + 1) + ')';
        }
      }
      parts.unshift(seg);
      node = parent;
    }
    return parts.join(' > ');
  }

  function labelFor(el) {
    if (el.labels && el.labels.length) return (el.labels[0].textContent || '').trim();
    var aria = el.getAttribute && el.getAttribute('aria-label');
    if (aria) return aria.trim();
    if (el.closest) {
      var wrap = el.closest('label');
      if (wrap) return (wrap.textContent || '').trim();
    }
    var ph = el.getAttribute && el.getAttribute('placeholder');
    if (ph) return ph.trim();
    var name = el.getAttribute && el.getAttribute('name');
    if (name) {
      var lbl = document.querySelector('label[for="' + CSS.escape(name) + '"]');
      if (lbl) return (lbl.textContent || '').trim();
    }
    return '';
  }

  function deepQueryAll(sel, root, budget) {
    var results = [];
    var seen = new Set();
    var work = 0;
    function walk(scope) {
      if (budget && work >= budget) return;
      var items = scope.querySelectorAll(sel);
      for (var i = 0; i < items.length; i++) {
        if (budget && work >= budget) return;
        if (!seen.has(items[i])) { seen.add(items[i]); results.push(items[i]); }
      }
      var all = scope.querySelectorAll('*');
      for (var j = 0; j < all.length; j++) {
        if (budget && work >= budget) return;
        work++;
        var el = all[j];
        if (el.shadowRoot) walk(el.shadowRoot);
        if (el.tagName === 'IFRAME') {
          try { if (el.contentDocument) walk(el.contentDocument); } catch (e) {}
        }
      }
    }
    walk(root || document);
    return results;
  }

  bmcp.q = function (sel, root) {
    var el = (root || document).querySelector(sel);
    if (el) return el;
    var deep = deepQueryAll(sel, root);
    if (deep.length) return deep[0];
    throw new Error('No element matched selector: ' + sel);
  };

  bmcp.qAll = function (sel, root) {
    var direct = Array.prototype.slice.call((root || document).querySelectorAll(sel));
    if (direct.length) return direct;
    return deepQueryAll(sel, root);
  };

  bmcp.deepQuery = function (sel, root) {
    return deepQueryAll(sel, root);
  };

  bmcp.visible = function (el) {
    if (!el) return false;
    var s = getComputedStyle(el);
    if (s.display === 'none' || s.visibility === 'hidden' || s.opacity === '0') return false;
    var r = el.getBoundingClientRect();
    return r.width > 0 && r.height > 0;
  };

  bmcp.scrollIntoView = function (el) {
    el.scrollIntoView({ block: 'center', behavior: 'smooth' });
    return true;
  };

  bmcp.info = function (el) {
    if (!el) return null;
    var idf = bmcp._idFreq || (bmcp._idFreq = idFrequency());
    var tag = (el.tagName || '').toLowerCase();
    var o = {
      tag: tag,
      selector: cssPath(el, idf),
      id: el.id || '',
      name: (el.getAttribute && el.getAttribute('name')) || '',
      label: labelFor(el),
      ariaLabel: (el.getAttribute && el.getAttribute('aria-label')) || '',
      placeholder: (el.getAttribute && el.getAttribute('placeholder')) || '',
      type: el.type || '',
      value: el.value !== undefined ? el.value : ((el.textContent || '').slice(0, 500)),
      checked: el.checked !== undefined ? !!el.checked : undefined,
      required: el.required !== undefined ? !!el.required : undefined,
      readonly: el.readOnly !== undefined ? !!el.readOnly : undefined,
      disabled: el.disabled !== undefined ? !!el.disabled : undefined,
      visible: bmcp.visible(el),
    };
    if (el.id && idf[el.id] > 1) o.duplicateId = true;
    if (el.type === 'file') o.fileInput = true;
    if (el.getAttribute && el.getAttribute('autocomplete')) o.autocomplete = el.getAttribute('autocomplete');
    if (el.maxLength !== undefined && el.maxLength >= 0) o.maxLength = el.maxLength;
    if (tag === 'select') {
      o.options = Array.prototype.map.call(el.options || [], function (op) {
        return { value: op.value, label: (op.textContent || '').trim() };
      });
      o.selectedIndex = el.selectedIndex;
    }
    return o;
  };

  bmcp.fields = function (scope, opts) {
    opts = opts || {};
    var root = scope ? (typeof scope === 'string' ? bmcp.q(scope) : scope) : document;
    // Fresh id frequency so dynamic rows re-resolve; bounded walk so heavy
    // pages (giant forms, lots of shadow roots) don't hang the call.
    bmcp._idFreq = idFrequency();
    var els = deepQueryAll('input, textarea, select, button, [contenteditable="true"], [role="combobox"]', root, opts.maxWork || 25000);
    var out = [];
    var seen = new Set();
    for (var i = 0; i < els.length; i++) {
      var el = els[i];
      if (seen.has(el)) continue;
      seen.add(el);
      if (el.disabled && !opts.includeDisabled) continue;
      var t = (el.type || '').toLowerCase();
      if (t === 'hidden' && !opts.includeHidden) continue;
      out.push(bmcp.info(el));
      if (opts.maxFields && out.length >= opts.maxFields) break;
    }
    return out;
  };

  bmcp.findField = function (label) {
    var needle = String(label).trim().toLowerCase();
    function exact(l) { return (l.textContent || '').trim().toLowerCase(); }
    var labels = Array.prototype.slice.call(document.querySelectorAll('label'));
    var candidates = labels.filter(function (l) { return exact(l) === needle; });
    if (!candidates.length) {
      candidates = labels.filter(function (l) { return exact(l).indexOf(needle) !== -1 || needle.indexOf(exact(l)) !== -1; });
    }
    if (candidates.length) {
      var l = candidates[0];
      if (l.htmlFor) {
        var byFor = document.getElementById(l.htmlFor);
        if (byFor) return byFor;
      }
      var inner = l.querySelector('input, select, textarea, button');
      if (inner) return inner;
    }
    var aria = bmcp.qAll('[aria-label]').filter(function (el) {
      return (el.getAttribute('aria-label') || '').trim().toLowerCase() === needle;
    })[0];
    if (aria) return aria;
    var ph = bmcp.qAll('[placeholder]').filter(function (el) {
      return (el.getAttribute('placeholder') || '').trim().toLowerCase() === needle;
    })[0];
    if (ph) return ph;
    var byName = bmcp.qAll('[name]').filter(function (el) {
      return String(el.getAttribute('name')).toLowerCase() === needle;
    })[0];
    if (byName) return byName;
    throw new Error('No field matched label: ' + label);
  };

  function setNativeValue(el, value) {
    var proto = el.tagName === 'TEXTAREA' ? window.HTMLTextAreaElement.prototype
      : (el.tagName === 'SELECT' ? window.HTMLSelectElement.prototype : window.HTMLInputElement.prototype);
    var desc = Object.getOwnPropertyDescriptor(proto, 'value');
    if (desc && desc.set) {
      desc.set.call(el, value);
    } else {
      el.value = value;
    }
  }

  bmcp.setValue = function (el, value) {
    if (!el) throw new Error('setValue: no element');
    if (el.tagName === 'SELECT') {
      setNativeValue(el, value);
      el.dispatchEvent(new Event('change', { bubbles: true }));
      return el.value;
    }
    if (el.type === 'checkbox' || el.type === 'radio') {
      el.checked = !!value;
      el.dispatchEvent(new Event('change', { bubbles: true }));
      el.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
      return el.checked;
    }
    if (el.isContentEditable) {
      el.textContent = String(value);
      el.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: String(value) }));
      return el.textContent;
    }
    if (el.type === 'file') {
      throw new Error('file inputs cannot be set with setValue; use browser_upload_file (or bmcp.uploadFile)');
    }
    setNativeValue(el, String(value));
    el.dispatchEvent(new Event('input', { bubbles: true }));
    el.dispatchEvent(new Event('change', { bubbles: true }));
    return el.value;
  };

  function placeCaretAtEnd(el) {
    if (el.setSelectionRange) {
      var len = el.value ? el.value.length : 0;
      try { el.setSelectionRange(len, len); } catch (e) {}
    } else if (el.isContentEditable) {
      var range = document.createRange();
      range.selectNodeContents(el);
      range.collapse(false);
      var sel = window.getSelection();
      sel.removeAllRanges();
      sel.addRange(range);
    }
  }

  function pressKey(el, key, opts) {
    opts = opts || {};
    var k = { key: key, code: opts.code || '', bubbles: true, cancelable: true };
    if (key === 'Enter') { k.keyCode = 13; k.which = 13; }
    else if (key === 'Tab') { k.keyCode = 9; k.which = 9; }
    else if (key === 'Escape') { k.keyCode = 27; k.which = 27; }
    else if (key === 'Backspace') { k.keyCode = 8; k.which = 8; }
    el.dispatchEvent(new KeyboardEvent('keydown', k));
    el.dispatchEvent(new KeyboardEvent('keyup', k));
  }

  bmcp.key = function (el, key, opts) {
    el.focus();
    pressKey(el, key, opts);
    return true;
  };

  bmcp.pressKey = pressKey;

  bmcp.findByText = function (sel, text, scope) {
    var els = bmcp.qAll(sel, scope);
    var re = text instanceof RegExp ? text : new RegExp(String(text), 'i');
    for (var i = 0; i < els.length; i++) {
      var label = (els[i].textContent || els[i].value || '').trim();
      if (re.test(label)) return els[i];
    }
    throw new Error('No element matched text: ' + text);
  };

  bmcp.buttons = function (scope) {
    var root = scope ? (typeof scope === 'string' ? bmcp.q(scope) : scope) : document;
    var els = root.querySelectorAll('button, [role="button"], input[type="button"], input[type="submit"], a[href]');
    var out = [];
    var seen = new Set();
    for (var i = 0; i < els.length; i++) {
      var el = els[i];
      if (seen.has(el)) continue;
      seen.add(el);
      var label = (el.textContent || el.value || '').trim();
      out.push({ text: label, selector: cssPath(el), visible: bmcp.visible(el), disabled: !!el.disabled });
    }
    return out;
  };

  bmcp.snapshot = function (opts) {
    opts = opts || {};
    var fields = bmcp.fields(null, { includeHidden: !!opts.includeHidden, includeDisabled: !!opts.includeDisabled });
    var buttons = bmcp.buttons();
    var headings = [];
    var hs = document.querySelectorAll('h1, h2, h3');
    for (var i = 0; i < hs.length && i < 20; i++) headings.push((hs[i].textContent || '').trim());
    return {
      url: location.href,
      title: document.title,
      fields: fields.slice(0, opts.maxFields || 100),
      buttons: buttons.slice(0, opts.maxButtons || 100),
      headings: headings,
      text: bmcp.text(null, opts.maxText || 4000),
    };
  };

  bmcp.type = function (el, text, opts) {
    opts = opts || {};
    var mode = opts.mode || 'insertText';
    el.focus();
    if (opts.scroll !== false) { try { el.scrollIntoView({ block: 'center' }); } catch (e) {} }

    if (el.tagName === 'SELECT') {
      bmcp.setValue(el, text);
      if (opts.pressEnter) pressKey(el, 'Enter');
      return true;
    }
    if (el.type === 'checkbox' || el.type === 'radio') {
      bmcp.click(el);
      return true;
    }
    if (el.type === 'file') {
      throw new Error('file inputs cannot be typed into; use browser_upload_file (or bmcp.uploadFile)');
    }

    var s = String(text);
    if (mode === 'native') {
      bmcp.setValue(el, s);
      if (opts.pressEnter) pressKey(el, 'Enter');
      return true;
    }

    if (mode === 'keyboard') {
      var current = opts.clearFirst ? '' : (el.value || '');
      if (opts.clearFirst) setNativeValue(el, '');
      for (var i = 0; i < s.length; i++) {
        var ch = s[i];
        current += ch;
        setNativeValue(el, current);
        el.dispatchEvent(new KeyboardEvent('keydown', { key: ch, bubbles: true, cancelable: true }));
        el.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: ch }));
        el.dispatchEvent(new KeyboardEvent('keyup', { key: ch, bubbles: true }));
      }
      el.dispatchEvent(new Event('change', { bubbles: true }));
      if (opts.pressEnter) pressKey(el, 'Enter');
      return true;
    }

    // insertText: the framework-friendly default. Replaces the selection.
    if (opts.clearFirst) {
      try { el.select(); } catch (e) { setNativeValue(el, ''); }
    } else {
      placeCaretAtEnd(el);
    }
    if (el.isContentEditable) placeCaretAtEnd(el);
    document.execCommand('insertText', false, s);
    el.dispatchEvent(new Event('change', { bubbles: true }));
    if (opts.pressEnter) pressKey(el, 'Enter');
    return true;
  };

  bmcp.click = function (el) {
    if (!el) throw new Error('click: no element');
    try { el.scrollIntoView({ block: 'center' }); } catch (e) {}
    el.click();
    return true;
  };

  bmcp.get = function (el) {
    if (!el) return null;
    if (el.isContentEditable) return el.textContent;
    if (el.type === 'checkbox' || el.type === 'radio') return el.checked;
    if (el.tagName === 'SELECT') return el.value;
    return el.value;
  };

  bmcp.uploadFile = function (b64, fileName, mimeType, selector) {
    var input = null;
    if (selector) input = bmcp.q(selector);
    if (!input || input.type !== 'file') {
      var focused = document.activeElement;
      if (focused && focused.type === 'file') {
        input = focused;
      } else if (!input) {
        var first = document.querySelector('input[type="file"]');
        if (first) input = first;
      }
    }
    if (!input) throw new Error('No file input found' + (selector ? ' for selector: ' + selector : ''));
    if (input.type !== 'file') throw new Error('Selector does not point at a file input: ' + selector);

    var bin = atob(b64);
    var bytes = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    var file = new File([bytes], fileName, { type: mimeType || 'application/octet-stream' });
    var dt = new DataTransfer();
    dt.items.add(file);
    input.files = dt.files;
    input.dispatchEvent(new Event('change', { bubbles: true }));
    return {
      files: input.files.length,
      name: input.files[0] && input.files[0].name,
      size: input.files[0] && input.files[0].size,
      selector: cssPath(input),
    };
  };

  bmcp.text = function (scope, maxChars) {
    var root = scope ? (typeof scope === 'string' ? bmcp.q(scope) : scope) : document.body;
    var t = (root.innerText || root.textContent || '').replace(/\\u00a0/g, ' ');
    if (maxChars) t = t.slice(0, maxChars);
    return t;
  };

  bmcp.highlight = function (el, opts) {
    opts = opts || {};
    var color = opts.color || '#ff3b30';
    var ms = opts.ms || 1500;
    try { el.scrollIntoView({ block: 'center' }); } catch (e) {}
    var prev = { outline: el.style.outline, outlineOffset: el.style.outlineOffset, boxShadow: el.style.boxShadow };
    el.style.outline = '3px solid ' + color;
    el.style.outlineOffset = '2px';
    el.style.boxShadow = '0 0 0 4px ' + color + '33';
    setTimeout(function () {
      el.style.outline = prev.outline;
      el.style.outlineOffset = prev.outlineOffset;
      el.style.boxShadow = prev.boxShadow;
    }, ms);
    return true;
  };

  bmcp.wait = function (ms) {
    return new Promise(function (resolve) { setTimeout(resolve, ms); });
  };

  bmcp.waitFor = async function (sel, opts) {
    opts = opts || {};
    var timeout = opts.timeout || 10000;
    var interval = opts.interval || 150;
    var start = Date.now();
    while (Date.now() - start < timeout) {
      try {
        var el = bmcp.q(sel);
        if (el && bmcp.visible(el)) return el;
      } catch (e) {}
      await bmcp.wait(interval);
    }
    throw new Error('Timed out waiting for selector: ' + sel);
  };

  bmcp.state = window.__bmcpState || (window.__bmcpState = {});

  function serialize(x, depth) {
    depth = depth || 0;
    if (x === undefined || x === null) return null;
    var t = typeof x;
    if (t === 'string' || t === 'number' || t === 'boolean') return x;
    if (t === 'bigint') return x.toString() + 'n';
    if (t === 'function') return undefined;
    if (t === 'symbol') return String(x);
    if (x instanceof Error) return { __error: x.message, stack: x.stack };
    if (x instanceof Element || x instanceof Node) return bmcp.info(x);
    if (x instanceof Window) return { __type: 'window' };
    if (depth > 6) return { __depth: true };
    if (Array.isArray(x)) {
      var arr = [];
      for (var i = 0; i < x.length; i++) {
        var sv = serialize(x[i], depth + 1);
        if (sv !== undefined) arr.push(sv);
      }
      return arr;
    }
    if (t === 'object') {
      var out = {};
      var keys = Object.keys(x);
      for (var j = 0; j < keys.length; j++) {
        var k = keys[j];
        var v;
        try { v = x[k]; } catch (e) { out[k] = { __unserializable: true }; continue; }
        var sv2 = serialize(v, depth + 1);
        if (sv2 !== undefined) out[k] = sv2;
      }
      return out;
    }
    return undefined;
  }

  bmcp.serialize = serialize;
  bmcp.version = '1.0.0';

  window.__bmcp = bmcp;
})();
`;