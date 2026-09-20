// Return every named field's current value in one call (verify a filled form). Pass args.selector to scope to a form/container.
const root = args.selector ? bmcp.q(args.selector) : document;
const checks = {};
root.querySelectorAll('input, textarea, select').forEach((el) => {
  const n = el.name || el.id;
  if (!n) return;
  if (!(n in checks)) {
    checks[n] = el.value || (el.selectedOptions && el.selectedOptions[0] ? el.selectedOptions[0].text : '');
  }
});
const fileInput = root.querySelector('input[type="file"]');
if (fileInput) checks[fileInput.name || fileInput.id || 'file'] = fileInput.files[0] && fileInput.files[0].name;
return checks;