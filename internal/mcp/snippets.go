package mcp

// Canned JavaScript snippets backing the thin wrapper tools. Each runs inside
// the same async runner as browser_execute_js, so `args` and `bmcp` are in
// scope and a returned promise is awaited.

const snippetListFields = `return bmcp.fields(args.selector || null, {
  includeHidden: !!args.includeHidden,
  includeDisabled: !!args.includeDisabled
});`

const snippetTypeText = `const el = bmcp.q(args.selector);
if (args.highlightMs) bmcp.highlight(el, { ms: args.highlightMs });
await bmcp.type(el, args.text, {
  mode: args.mode || 'insertText',
  clearFirst: !!args.clearFirst,
  pressEnter: !!args.pressEnter,
  delay: args.delay || 0
});
const info = bmcp.info(el);
if (args.returnSnapshot) return { field: info, snapshot: bmcp.snapshot({ maxFields: args.snapshotFields || 100 }) };
return info;`

const snippetSetValue = `const el = bmcp.q(args.selector);
bmcp.setValue(el, args.value);
const info = bmcp.info(el);
if (args.returnSnapshot) return { field: info, snapshot: bmcp.snapshot({ maxFields: args.snapshotFields || 100 }) };
return info;`

const snippetGetValue = `const el = bmcp.q(args.selector);
return { selector: args.selector, value: bmcp.get(el), info: bmcp.info(el) };`

const snippetReadText = `return bmcp.text(args.selector || null, args.maxChars || 0);`

const snippetClick = `const el = bmcp.q(args.selector);
if (args.highlightMs) bmcp.highlight(el, { ms: args.highlightMs });
bmcp.click(el);
return bmcp.info(el);`

const snippetSubmit = `let form;
if (args.selector) {
  const el = bmcp.q(args.selector);
  form = el.tagName === 'FORM' ? el : (el.form || el.closest('form'));
}
if (!form) form = document.querySelector('form');
if (!form) throw new Error('No form found on the page');
const btn = form.querySelector('button[type="submit"], input[type="submit"], button:not([type])');
if (btn) {
  if (args.highlightMs) bmcp.highlight(btn, { ms: args.highlightMs });
  bmcp.click(btn);
} else {
  form.submit();
}
return { submitted: true, formAction: form.action || '', method: (form.method || 'get') };`

const snippetWait = `if (args.selector) {
  await bmcp.waitFor(args.selector, { timeout: args.timeoutMs || 10000, interval: args.intervalMs || 150 });
  return { waitedFor: args.selector };
}
await bmcp.wait(args.ms || 1000);
return { waited: true };`

const snippetHighlight = `const el = bmcp.q(args.selector);
bmcp.highlight(el, { ms: args.ms || 1500, color: args.color || '#ff3b30' });
return true;`

const snippetSnapshot = `return bmcp.snapshot({
  maxFields: args.maxFields || 100,
  maxButtons: args.maxButtons || 100,
  maxText: args.maxText || 4000
});`

const snippetFind = `let el;
if (args.label) el = bmcp.findField(args.label);
else if (args.text) el = bmcp.findByText(args.scope || 'button, [role="button"], a[href], input[type="submit"], input[type="button"]', args.text);
else if (args.selector) el = bmcp.q(args.selector);
if (!el) throw new Error('browser_find: nothing matched ' + JSON.stringify(args));
if (args.highlightMs) bmcp.highlight(el, { ms: args.highlightMs, color: args.color || '#22c55e' });
return bmcp.info(el);`

const snippetClickButton = `const el = bmcp.findByText(args.scope || 'button, [role="button"], a[href], input[type="submit"], input[type="button"]', args.text);
if (args.highlightMs) bmcp.highlight(el, { ms: args.highlightMs, color: args.color || '#22c55e' });
bmcp.click(el);
return bmcp.info(el);`
