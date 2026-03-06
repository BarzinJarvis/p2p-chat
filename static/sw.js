/* P2P Chat Service Worker — v3.28 */
const CACHE = 'p2pchat-v3.42';
const APP_SHELL = [
  '/',
  '/manifest.json',
  '/sw.js',
  '/js/marked.min.js',
  '/js/highlight.min.js',
  '/js/highlight-dark.min.css',
  '/js/highlight-light.min.css',
];

/* Install — cache app shell */
self.addEventListener('install', e => {
  e.waitUntil(
    caches.open(CACHE).then(c => c.addAll(APP_SHELL)).then(() => self.skipWaiting())
  );
});

/* Activate — purge old caches */
self.addEventListener('activate', e => {
  e.waitUntil(
    caches.keys().then(keys =>
      Promise.all(keys.filter(k => k !== CACHE).map(k => caches.delete(k)))
    ).then(() => self.clients.claim())
  );
});

/* Fetch — network-first for WS/upload/API, cache-first for shell */
self.addEventListener('fetch', e => {
  const url = new URL(e.request.url);

  // Never intercept WebSocket upgrades or upload/delete APIs
  if (
    url.pathname.startsWith('/ws') ||
    url.pathname.startsWith('/upload') ||
    url.pathname.startsWith('/uploads/') ||
    e.request.method !== 'GET'
  ) return;

  // Network-first for Google Fonts (can fail offline gracefully)
  if (url.hostname === 'fonts.googleapis.com' || url.hostname === 'fonts.gstatic.com') {
    e.respondWith(
      caches.open(CACHE).then(async c => {
        try {
          const fresh = await fetch(e.request);
          c.put(e.request, fresh.clone());
          return fresh;
        } catch {
          return c.match(e.request);
        }
      })
    );
    return;
  }

  // Cache-first for app shell
  e.respondWith(
    caches.match(e.request).then(cached => {
      if (cached) return cached;
      return fetch(e.request).then(resp => {
        if (resp && resp.status === 200 && e.request.method === 'GET') {
          const clone = resp.clone();
          caches.open(CACHE).then(c => c.put(e.request, clone));
        }
        return resp;
      }).catch(() => {
        // Only fall back to shell HTML for navigation requests — never for JS/CSS/fonts
        if (e.request.mode === 'navigate') return caches.match('/');
        return new Response('', { status: 503 });
      });
    })
  );
});
