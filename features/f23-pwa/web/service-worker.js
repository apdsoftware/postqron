const CACHE_PREFIX = "postqron-public-shell-";
const CACHE_NAME = `${CACHE_PREFIX}v1`;
const OFFLINE_URL = "/pwa/offline.html";
const PUBLIC_SHELL = [
  OFFLINE_URL,
  "/pwa/icon-192.png",
  "/pwa/icon-512.png",
  "/pwa/icon-maskable-512.png"
];

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME)
      .then((cache) => cache.addAll(PUBLIC_SHELL))
      .then(() => self.skipWaiting())
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys()
      .then((names) => Promise.all(
        names
          .filter((name) => name.startsWith(CACHE_PREFIX) && name !== CACHE_NAME)
          .map((name) => caches.delete(name))
      ))
      .then(() => self.clients.claim())
  );
});

// Only a fixed, public shell is pre-cached. Authenticated pages, API responses,
// request bodies, query-specific HTML and runtime navigation responses are
// never written to Cache Storage.
self.addEventListener("fetch", (event) => {
  const request = event.request;
  if (request.method !== "GET" || request.mode !== "navigate") {
    return;
  }
  const target = new URL(request.url);
  if (target.origin !== self.location.origin ||
      target.pathname.startsWith("/api/") ||
      target.pathname.startsWith("/auth/")) {
    return;
  }
  event.respondWith(
    fetch(request).catch(() => caches.match(OFFLINE_URL))
  );
});

self.addEventListener("push", (event) => {
  let payload = {};
  try {
    payload = event.data ? event.data.json() : {};
  } catch {
    payload = {};
  }
  const title = typeof payload.title === "string"
    ? payload.title.slice(0, 80)
    : "Postqron";
  const body = typeof payload.body === "string"
    ? payload.body.slice(0, 240)
    : "Hai un aggiornamento.";
  const tag = typeof payload.tag === "string"
    ? payload.tag.slice(0, 160)
    : "postqron-update";
  const url = safeRelativeURL(payload.url);
  event.waitUntil(self.registration.showNotification(title, {
    body,
    tag,
    renotify: false,
    icon: "/pwa/icon-192.png",
    badge: "/pwa/icon-192.png",
    data: { url }
  }));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const path = safeRelativeURL(event.notification.data?.url);
  event.waitUntil(
    self.clients.matchAll({ type: "window", includeUncontrolled: true })
      .then(async (windows) => {
        const target = new URL(path, self.location.origin).href;
        for (const windowClient of windows) {
          if (windowClient.url === target && "focus" in windowClient) {
            return windowClient.focus();
          }
        }
        return self.clients.openWindow(path);
      })
  );
});

self.addEventListener("pushsubscriptionchange", (event) => {
  event.waitUntil(
    self.clients.matchAll({ type: "window", includeUncontrolled: true })
      .then((windows) => Promise.all(
        windows.map((windowClient) => windowClient.postMessage({
          type: "postqron:push-subscription-expired"
        }))
      ))
  );
});

function safeRelativeURL(value) {
  if (typeof value !== "string" || !value.startsWith("/") ||
      value.startsWith("//")) {
    return "/";
  }
  try {
    const parsed = new URL(value, self.location.origin);
    return parsed.origin === self.location.origin
      ? `${parsed.pathname}${parsed.search}${parsed.hash}`
      : "/";
  } catch {
    return "/";
  }
}
