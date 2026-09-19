// Click a named atproto-qr.app button by its exact visible text (e.g. "Generate", "Save changes", "Download PNG").
const el = bmcp.findByText('main button', '^' + args.text + '$');
bmcp.highlight(el, { ms: args.highlightMs || 1200, color: '#22c55e' });
bmcp.click(el);
await bmcp.wait(args.waitMs || 1200);
return { clicked: args.text, text: (el.textContent || '').trim() };