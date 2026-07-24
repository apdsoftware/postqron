import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { PostqronPWA, urlBase64ToUint8Array } from "../web/pwa-client.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));

test("manifest has installable PNG icons and an offline-safe scope", async () => {
  const manifest = JSON.parse(await readFile(`${root}/web/manifest.webmanifest`, "utf8"));
  assert.equal(manifest.display, "standalone");
  assert.equal(manifest.scope, "/");
  assert.equal(manifest.start_url, "/?source=pwa");
  const expected = new Map([
    ["/pwa/icon-192.png", [192, 192]],
    ["/pwa/icon-512.png", [512, 512]],
    ["/pwa/icon-maskable-512.png", [512, 512]]
  ]);
  for (const icon of manifest.icons) {
    const dimensions = expected.get(icon.src);
    assert.ok(dimensions, `unexpected icon ${icon.src}`);
    assert.equal(icon.type, "image/png");
    const bytes = await readFile(`${root}/web/${icon.src.split("/").at(-1)}`);
    assert.deepEqual([...bytes.subarray(0, 8)], [137, 80, 78, 71, 13, 10, 26, 10]);
    assert.equal(bytes.readUInt32BE(16), dimensions[0]);
    assert.equal(bytes.readUInt32BE(20), dimensions[1]);
  }
  assert.equal(expected.size, manifest.icons.length);
  assert.ok(manifest.icons.some((icon) => icon.purpose === "maskable"));
});

test("service worker caches only fixed public shell assets", async () => {
  const worker = await readFile(`${root}/web/service-worker.js`, "utf8");
  assert.match(worker, /cache\.addAll\(PUBLIC_SHELL\)/);
  assert.match(worker, /request\.mode !== "navigate"/);
  assert.match(worker, /target\.pathname\.startsWith\("\/api\/"\)/);
  assert.match(worker, /fetch\(request\)\.catch\(\(\) => caches\.match\(OFFLINE_URL\)\)/);
  assert.doesNotMatch(worker, /cache\.put\(/);
  assert.doesNotMatch(worker, /caches\.match\(request\)/);
});

test("push permission is requested only by explicit enable and revoke reaches backend first", async () => {
  let permissionRequests = 0;
  let unsubscribed = false;
  const calls = [];
  const subscription = {
    endpoint: "https://push.example.test/device",
    toJSON: () => ({
      endpoint: "https://push.example.test/device",
      expirationTime: null,
      keys: { p256dh: "key", auth: "auth" }
    }),
    unsubscribe: async () => {
      unsubscribed = true;
      return true;
    }
  };
  const registration = {
    pushManager: {
      getSubscription: async () => subscription,
      subscribe: async () => {
        throw new Error("existing subscription should be reused");
      }
    }
  };
  const navigatorRef = {
    serviceWorker: {
      register: async () => registration,
      getRegistration: async () => registration
    }
  };
  const Notification = {
    permission: "default",
    requestPermission: async () => {
      permissionRequests++;
      return "granted";
    }
  };
  const fetchFn = async (url, options) => {
    calls.push([url, options]);
    return {
      ok: true,
      json: async () => ({ id: "subscription-1" })
    };
  };
  const client = new PostqronPWA({
    navigatorRef,
    windowRef: { PushManager: class {}, Notification },
    fetchFn
  });
  assert.equal(permissionRequests, 0);
  await client.enablePush("AQID");
  assert.equal(permissionRequests, 1);
  assert.equal(calls[0][1].method, "POST");
  assert.deepEqual(JSON.parse(calls[0][1].body), {
    endpoint: "https://push.example.test/device",
    expiration_time: null,
    keys: { p256dh: "key", auth: "auth" }
  });

  await client.disablePush();
  assert.equal(calls[1][1].method, "DELETE");
  assert.match(calls[1][1].body, /push\.example\.test/);
  assert.equal(unsubscribed, true);
});

test("VAPID public keys use URL-safe base64 decoding", () => {
  assert.deepEqual([...urlBase64ToUint8Array("AQID-_8")], [1, 2, 3, 251, 255]);
});
