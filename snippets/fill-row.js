// Fill fields inside a row container by name — for cloned dynamic rows whose
// ids repeat. args.container is a CSS selector for one row; args.values maps
// field name -> value. Uses bmcp.setValue (framework-safe events).
const container = bmcp.q(args.container);
const results = {};
for (const name in (args.values || {})) {
  const el = container.querySelector('[name="' + name + '"]');
  if (!el) {
    results[name] = { set: false, error: 'no element with name ' + name };
    continue;
  }
  bmcp.setValue(el, args.values[name]);
  results[name] = { set: true, value: bmcp.get(el) };
}
return results;