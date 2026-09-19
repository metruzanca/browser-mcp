"use strict";

// Dev-time sanity check for the injected BMCP_RUNTIME: extracts the runtime
// string from background.js, evals it against a minimal mock DOM, and verifies
// the helper API behaves. Run with: node test_runtime.js

const fs = require("fs");
const path = require("path");

const src = fs.readFileSync(path.join(__dirname, "background.js"), "utf8");
const m = src.match(/const BMCP_RUNTIME = `([\s\S]*?)`;/);
if (!m) throw new Error("could not extract BMCP_RUNTIME from background.js");
const runtime = m[1];

// ---- minimal mock DOM ---------------------------------------------------
class FakeEl {
  constructor(tag, attrs = {}) {
    this.nodeType = 1;
    this.tagName = tag.toUpperCase();
    this.attrs = Object.assign({}, attrs);
    this.children = [];
    this.parentElement = null;
    this.textContent = "";
    this.value = "";
    this.checked = false;
    this.disabled = false;
    this.required = false;
    this.readOnly = false;
    this.style = {};
    this.shadowRoot = null;
    this.labels = null;
    this.events = {};
  }
  getAttribute(k) { return this.attrs[k] !== undefined ? this.attrs[k] : null; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  focus() { this.focused = true; }
  select() { this.selected = true; }
  scrollIntoView() { return true; }
  click() { this.clicked = (this.clicked || 0) + 1; this.dispatchEvent({ type: "click" }); }
  getBoundingClientRect() { return { width: 100, height: 20 }; }
  dispatchEvent(ev) { (this.events[ev.type] = this.events[ev.type] || []).push(ev); }
  querySelectorAll() { return []; }
  get isContentEditable() { return this.attrs.contenteditable === "true"; }
  get type() { return this.attrs.type || (this.tagName === "INPUT" ? "text" : ""); }
  get id() { return this.attrs.id || ""; }
  get maxLength() { return -1; }
}

const windowMock = {};
const fakeBody = new FakeEl("body");
let labelRegistry = [];

const documentMock = {
  title: "Test page",
  body: fakeBody,
  querySelector(sel) { return null; },
  querySelectorAll(sel) { return []; },
  getElementById() { return null; },
  createRange() { return { selectNodeContents() {}, collapse() {} }; },
  execCommand() { return false; },
  createEvent() { return { initEvent() {} }; },
};

const CSS = { escape: (s) => s };
const getComputedStyle = () => ({ display: "block", visibility: "visible", opacity: "1" });

function installGlobals(win, doc) {
  win.HTMLInputElement = { prototype: {} };
  win.HTMLTextAreaElement = { prototype: {} };
  win.HTMLSelectElement = { prototype: {} };
  win.__bmcpState = {};
  win.getSelection = () => ({ removeAllRanges() {}, addRange() {} });
}

// Run the runtime in a fresh sandbox via eval.
installGlobals(windowMock, documentMock);
const sandbox = {
  window: windowMock,
  document: documentMock,
  location: { href: "https://example.com/" },
  CSS,
  getComputedStyle,
  Event: class { constructor(t, o) { this.type = t; this.bubbles = o && o.bubbles; } },
  InputEvent: class { constructor(t, o) { this.type = t; this.data = o && o.data; this.inputType = o && o.inputType; } },
  KeyboardEvent: class { constructor(t, o) { this.type = t; this.key = o && o.key; } },
  MouseEvent: class { constructor(t, o) { this.type = t; } },
  PointerEvent: class { constructor(t, o) { this.type = t; } },
  Element: FakeEl,
  Node: FakeEl,
  Window: class {},
  Error,
  Promise,
  setTimeout,
  setInterval,
  clearTimeout,
  clearInterval,
  Date,
  Object,
  Array,
  Set,
  String,
  Number,
  Boolean,
  JSON,
  Math,
  RegExp,
  console,
};
// The runtime IIFE references `window`, `document`, `CSS`, `getComputedStyle`,
// `Event`, etc. as bare globals, so inject them into the global scope:
Object.assign(globalThis, sandbox);
eval(runtime);

const bmcp = globalThis.window.__bmcp;
if (!bmcp) throw new Error("__bmcp was not installed");

let pass = 0;
let fail = 0;
function check(name, cond) {
  if (cond) { pass++; console.log("ok  - " + name); }
  else { fail++; console.log("FAIL- " + name); }
}

// API surface
check("info", typeof bmcp.info === "function");
check("fields", typeof bmcp.fields === "function");
check("setValue", typeof bmcp.setValue === "function");
check("type", typeof bmcp.type === "function");
check("click", typeof bmcp.click === "function");
check("findField", typeof bmcp.findField === "function");
check("state", typeof bmcp.state === "object");
check("serialize", typeof bmcp.serialize === "function");

// info on an input
const input = new FakeEl("input", { name: "email", placeholder: "Email", id: "e1" });
input.value = "a@b.com";
const info = bmcp.info(input);
check("info.name", info.name === "email");
check("info.selector has id", info.selector === "#e1");
check("info.value", info.value === "a@b.com");

// setValue dispatch
input.value = "";
const before = (input.events["input"] || []).length;
bmcp.setValue(input, "hello");
check("setValue sets value", input.value === "hello");
check("setValue fires input", (input.events["input"] || []).length === before + 1);
check("setValue fires change", (input.events["change"] || []).length === 1);

// select setValue
const sel = new FakeEl("select");
sel.value = "";
bmcp.setValue(sel, "opt2");
check("setValue select", sel.value === "opt2");

// click
const btn = new FakeEl("button");
bmcp.click(btn);
check("click calls el.click", btn.clicked === 1);

// text
fakeBody.innerText = "Hello\nworld";
check("text body", bmcp.text(null) === "Hello\nworld");
check("text maxChars", bmcp.text(null, 5) === "Hello");

// serialize
check("serialize primitive", bmcp.serialize(42) === 42);
check("serialize node -> info", bmcp.serialize(input).name === "email");
const arr = bmcp.serialize([1, input, { a: null, fn: () => {} }]);
check("serialize array drops function", arr[1].tag === "input" && Object.keys(arr[2]).length === 1);

// type (insertText path uses document.execCommand stub + setValue semantics)
const tinput = new FakeEl("input", { type: "text" });
bmcp.type(tinput, "typed!", { mode: "insertText", clearFirst: true });
check("type runs without throwing", true);

// --- new helpers ---
check("findByText", typeof bmcp.findByText === "function");
check("buttons", typeof bmcp.buttons === "function");
check("snapshot", typeof bmcp.snapshot === "function");
check("pressKey", typeof bmcp.pressKey === "function");

// Wire the fake button into the mock DOM, then test buttons/findByText/snapshot.
const fakeBtn = new FakeEl("button");
fakeBtn.textContent = "Save changes";
documentMock.querySelectorAll = (sel) => {
  if (sel.includes("button")) return [fakeBtn];
  if (sel === "h1, h2, h3") return [];
  return [];
};
fakeBody.querySelectorAll = documentMock.querySelectorAll;

const btns = bmcp.buttons();
check("buttons returns button text", btns.length === 1 && btns[0].text === "Save changes");

let found = null;
try {
  found = bmcp.findByText("button", "save", fakeBody);
} catch (e) {
  found = null;
}
check("findByText finds by text", found === fakeBtn);

const snap = bmcp.snapshot({ maxText: 50 });
check("snapshot buttons populated", Array.isArray(snap.buttons) && snap.buttons.length === 1);
check("snapshot text string", typeof snap.text === "string");
check("snapshot fields array", Array.isArray(snap.fields));

console.log("\n" + pass + " passed, " + fail + " failed");
process.exit(fail ? 1 : 0);