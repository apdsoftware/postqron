export class PostqronPWA {
  #navigator;
  #window;
  #fetch;
  #installPrompt = null;

  constructor({
    navigatorRef = globalThis.navigator,
    windowRef = globalThis.window,
    fetchFn = globalThis.fetch
  } = {}) {
    this.#navigator = navigatorRef;
    this.#window = windowRef;
    this.#fetch = fetchFn;
  }

  get supported() {
    return Boolean(
      this.#navigator?.serviceWorker &&
      this.#window?.PushManager &&
      this.#window?.Notification
    );
  }

  listenForInstallPrompt(onAvailable) {
    this.#window?.addEventListener("beforeinstallprompt", (event) => {
      event.preventDefault();
      this.#installPrompt = event;
      onAvailable?.();
    });
  }

  async promptInstall() {
    if (!this.#installPrompt) {
      return { outcome: "unavailable" };
    }
    const prompt = this.#installPrompt;
    this.#installPrompt = null;
    await prompt.prompt();
    return prompt.userChoice;
  }

  async register() {
    if (!this.#navigator?.serviceWorker) {
      throw new Error("Service worker non supportato.");
    }
    return this.#navigator.serviceWorker.register("/service-worker.js", {
      scope: "/"
    });
  }

  // Call only from an explicit user action. This is the sole permission prompt.
  async enablePush(vapidPublicKey) {
    if (!this.supported) {
      throw new Error("Notifiche push non supportate.");
    }
    let permission = this.#window.Notification.permission;
    if (permission === "default") {
      permission = await this.#window.Notification.requestPermission();
    }
    if (permission !== "granted") {
      throw new Error("Permesso per le notifiche non concesso.");
    }
    const registration = await this.register();
    let subscription = await registration.pushManager.getSubscription();
    if (!subscription) {
      subscription = await registration.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(vapidPublicKey)
      });
    }
    const serialized = subscription.toJSON();
    const response = await this.#fetch("/api/v1/push/subscriptions", {
      method: "POST",
      credentials: "same-origin",
      headers: {
        "Content-Type": "application/json",
        "X-Requested-With": "PostqronPWA"
      },
      body: JSON.stringify({
        endpoint: serialized.endpoint,
        expiration_time: serialized.expirationTime,
        keys: serialized.keys
      })
    });
    if (!response.ok) {
      throw new Error("Impossibile attivare le notifiche push.");
    }
    return response.json();
  }

  async disablePush() {
    if (!this.#navigator?.serviceWorker) {
      return false;
    }
    const registration = await this.#navigator.serviceWorker.getRegistration("/");
    const subscription = await registration?.pushManager.getSubscription();
    if (!subscription) {
      return false;
    }
    const response = await this.#fetch("/api/v1/push/subscriptions", {
      method: "DELETE",
      credentials: "same-origin",
      headers: {
        "Content-Type": "application/json",
        "X-Requested-With": "PostqronPWA"
      },
      body: JSON.stringify({ endpoint: subscription.endpoint })
    });
    if (!response.ok) {
      throw new Error("Impossibile revocare le notifiche push.");
    }
    await subscription.unsubscribe();
    return true;
  }
}

export function urlBase64ToUint8Array(value) {
  const padding = "=".repeat((4 - value.length % 4) % 4);
  const base64 = (value + padding).replaceAll("-", "+").replaceAll("_", "/");
  const decode = globalThis.atob ??
    ((encoded) => Buffer.from(encoded, "base64").toString("binary"));
  const raw = decode(base64);
  return Uint8Array.from(raw, (character) => character.charCodeAt(0));
}
