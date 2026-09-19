// Fill the atproto-qr.app URL content field (types into the input with the https://example.com placeholder).
const el = bmcp.q('input[placeholder="https://example.com"]');
bmcp.highlight(el, { ms: args.highlightMs || 1200, color: '#22c55e' });
await bmcp.type(el, args.url, { clearFirst: true });
await bmcp.wait(800);
return { value: el.value };