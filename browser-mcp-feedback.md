# browser-mcp Feedback

Notes from using browser-mcp (extension + Go daemon) to drive a Citadel Securities
job application form (Gild ATS embedded on citadelsecurities.com). The form had
required file upload, repeated dynamic rows (Add Experience), selects, date
inputs, and a heavy page main thread.

## What worked well

- `browser_set_value` on plain text inputs, selects, dates, and textareas is
  reliable. It dispatches both `input` and `change`, which the Gild form needed.
- `bmcp.state` persists across calls. Useful for anything that needs to be
  accumulated in small steps.
- `browser_find` / `browser_click` handled the "Start Application" reveal
  button cleanly.
- Pinning to a tab with `browser_set_target` meant the session stayed on the
  Citadel tab while the user browsed elsewhere.

## Problems and proposed improvements

### 1. No way to set a file input

`browser_set_value` (and `bmcp.setValue`) throws `InvalidStateError` on
`<input type="file">`, and there is no upload tool. Workaround below.

**Recommendation:** add a native `browser_upload_file` tool that accepts a local
path and a selector (or the currently focused input), implemented in the daemon
via the extension. Without CDP access this needs an extension-side file picker
hack, but any first-class path is better than the JS workaround.

### 2. Large payloads get truncated in transit

Passing a base64 PDF (~53 KB) through `args` in `browser_execute_js` was
unreliable: several calls came back as JSON parse errors or stored strings 4-8
characters short, silently corrupting the file.

**Recommendation:** raise or document the argument size limit, and either
(a) reject oversized args with a clear error instead of truncating, or
(b) support reading a local file by path from the daemon so the agent never has
to push big blobs through tool arguments.

### 3. No return value from `browser_run_snippet`

The snippet that set the file input ran fine but returned `"ok"` instead of the
JSON the snippet produced. Inconsistent with `browser_execute_js`, which returns
the value.

**Recommendation:** make `browser_run_snippet` surface the snippet's return
value the same way `browser_execute_js` does.

### 4. Timeouts on busy page threads

This page had a heavy main thread (Gild form + Speechify). `execute_js`,
`list_fields`, and `read_text` frequently returned "The browser did not respond
in time" while the page was busy. No automatic retry; each failure cost a wait
cycle. `snapshot` was the most resilient call and succeeded when others hung.

**Recommendation:** add automatic retry with backoff for transient timeouts, and
make the timeout configurable per call. Also consider that `list_fields` and
`read_text` can be very slow on large forms; an option to cap work would help.

### 5. Duplicate IDs in dynamically added rows

"Add Experience" cloned rows where every row had the same element ids
(`#profile_companies__title`, etc.). After adding a row, targeting by id was
ambiguous and `browser_set_value` could hit the wrong row. Only the first row's
inputs had unique `name` values at snapshot time.

**Recommendation:** return row-qualified selectors (the full CSS path) in
`list_fields` output for every element, not just the first match, so agents can
disambiguate. Also warn when ids are duplicated.

### 6. `bmcp.setValue` has no file-input branch

`setNativeValue` throws for file inputs. A targeted check for `el.type ===
'file'` that surfaces a clear "use the upload tool" message (or performs the
upload) would turn a confusing exception into a guidance path.

## JS patterns used

### Set a file input without an upload tool

The reliable variant read the file bytes from a snippet file on disk, so the
base64 never travelled through tool arguments:

```js
(async () => {
  const b64 = "<base64 of file>";
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  const file = new File([bytes], 'resume-sam-zanca.pdf', { type: 'application/pdf' });
  const input = document.querySelector('#resume');
  const dt = new DataTransfer();
  dt.items.add(file);
  input.files = dt.files;
  input.dispatchEvent(new Event('change', { bubbles: true }));
  return { files: input.files.length, name: input.files[0] && input.files[0].name, size: input.files[0] && input.files[0].size };
})();
```

Where the daemon runs snippets from disk (e.g. `~/.config/browser-mcp/snippets/`),
write the snippet there with `bash` and call `browser_run_snippet` with no args.
This avoids both the file-input restriction and argument truncation.

### Accumulate large data in `bmcp.state`

If you must push data through `args`, split it and reassemble:

```js
// store per part
bmcp.state.resumeParts = bmcp.state.resumeParts || [];
bmcp.state.resumeParts[0] = args.b64;   // repeat for each part
return { part: 0, len: args.b64.length, stored: bmcp.state.resumeParts.filter(Boolean).length };

// assemble later
const parts = bmcp.state.resumeParts;
const b64 = parts.join('');
const bin = atob(b64);
const bytes = new Uint8Array(bin.length);
for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
// ...build File, set on input...
```

Caveat: verify reassembled length against the source file; truncation slipped
through silently and produced a file 3 bytes short.

### Fill a dynamically added row unambiguously

Scope the query to the row container rather than trusting ids:

```js
const container = document.querySelector(
  '#gild-form-combined > div > div:nth-of-type(1) > div > div:nth-of-type(6) > div > fieldset > div > div:nth-of-type(2)'
);
const set = (el, val) => {
  const proto = el.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, val);
  el.dispatchEvent(new Event('input', { bubbles: true }));
  el.dispatchEvent(new Event('change', { bubbles: true }));
};
set(container.querySelector('input[name="profile[companies][new1][title]"]'), 'Senior Engineer');
set(container.querySelector('input[name="profile[companies][new1][name]"]'), 'Chainalysis');
// ...dates, textarea...
```

Using the native value setter plus `input` + `change` events is what makes the
form framework accept the value. This is the same mechanism `bmcp.setValue`
uses, applied to an element found by `name` inside a row container.

### Verify a whole form in one call

After filling, snapshot all named fields in one `execute_js` call to confirm
nothing was missed or cleared:

```js
const f = document.querySelector('#gild-form-combined');
const checks = {};
f.querySelectorAll('input, textarea, select').forEach(el => {
  const n = el.name;
  if (!n) return;
  if (!(n in checks)) checks[n] = el.value || el.selectedOptions?.[0]?.text || '';
});
checks.resume = f.querySelector('#resume').files[0]?.name;
return checks;
```