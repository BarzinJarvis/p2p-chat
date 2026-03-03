/* P2P Chat Service Worker — v3.6 */
const CACHE = 'p2pchat-v3.16';
const APP_SHELL = [
  '/',
  '/manifest.json',
  '/icons/icon-192.png',
  '/icons/icon-512.png',
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
      }).catch(() => caches.match('/'));
    })
  );
});
