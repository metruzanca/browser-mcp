// Switch the atproto-qr.app code type between Fixed and Dynamic (args.type: "fixed"|"dynamic").
const btn = bmcp.findByText('main button', '^' + (args.type || 'dynamic') + '$');
bmcp.click(btn);
await bmcp.wait(400);
return { type: args.type || 'dynamic', pressed: btn.getAttribute('aria-pressed') };