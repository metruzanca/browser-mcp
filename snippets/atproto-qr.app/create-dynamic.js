// Create a Dynamic QR on atproto-qr.app encoding args.url, name it, and save it. Must run on the editor page (use open-editor first). Confirmed 2026-09-19.
const url = args.url || 'zanca.dev/blog';
if (!bmcp.q('input[placeholder="https://example.com"]')) throw new Error('not on the atproto-qr.app editor; run atproto-qr.app/open-editor first');
// 1. Switch code type to Dynamic
bmcp.click(bmcp.findByText('main button', '^dynamic$'));
await bmcp.wait(400);
// 2. Fill the URL content field
await bmcp.type(bmcp.q('input[placeholder="https://example.com"]'), url, { clearFirst: true });
await bmcp.wait(600);
// 3. Generate the code (creates the dynamic slug + QR preview)
bmcp.click(bmcp.findByText('main button', '^generate$'));
await bmcp.wait(1500);
// 4. Name it (slug appears in the redirect URL)
const name = args.name || 'zanca-blog';
await bmcp.type(bmcp.q('input[placeholder="happy-otter"]'), name, { clearFirst: true });
// 5. Save changes
bmcp.click(bmcp.findByText('main button', '^save changes$'));
await bmcp.wait(1500);
return { name, url, saved: true };