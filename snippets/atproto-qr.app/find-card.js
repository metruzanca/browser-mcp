// Check whether a saved QR code with args.name appears on the atproto-qr.app "My QR codes" page.
const text = bmcp.text('main', 4000);
const names = [...document.querySelectorAll('main a, main article')]
  .map(el => (el.textContent || '').trim())
  .filter(t => t && t.length < 80);
return { found: text.includes(args.name), name: args.name, pageCards: names.slice(0, 20) };