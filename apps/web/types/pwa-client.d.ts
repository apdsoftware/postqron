declare module '*pwa-client.mjs' {
  export class PostqronPWA {
    readonly supported: boolean
    register(): Promise<ServiceWorkerRegistration>
    enablePush(vapidPublicKey: string): Promise<unknown>
    disablePush(): Promise<boolean>
    listenForInstallPrompt(onAvailable?: () => void): void
    promptInstall(): Promise<{ outcome: string }>
  }
}
