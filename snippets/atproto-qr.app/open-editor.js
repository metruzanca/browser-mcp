// Open the atproto-qr.app editor with a URL prefilled from the query string.
location.href = 'https://atproto-qr.app/?url=' + encodeURIComponent(args.url || '');
return true;