import type { CheckoutAction } from './billing.ts'

export const PADDLE_SCRIPT_URL = 'https://cdn.paddle.com/paddle/v2/paddle.js'

export interface PaddleEvent {
  name?: string
  data?: {
    transaction_id?: string
  }
}

export interface PaddleBrowser {
  Environment: {
    set(environment: 'sandbox'): void
  }
  Initialize(options: {
    token: string
    checkout: {
      settings: {
        displayMode: 'overlay'
        locale: string
        theme: 'light'
      }
    }
    eventCallback(event: PaddleEvent): void
  }): void
  Update?(options: {
    eventCallback(event: PaddleEvent): void
  }): void
  Checkout: {
    open(options: {
      transactionId: string
      settings: {
        displayMode: 'overlay'
        locale: string
        theme: 'light'
      }
    }): void
  }
}

export function checkoutActionForPaddleEvent(
  event: PaddleEvent,
): CheckoutAction | undefined {
  if (event.name === 'checkout.completed') {
    return 'completed'
  }
  if (
    event.name === 'checkout.payment.failed'
    || event.name === 'checkout.payment.error'
    || event.name === 'checkout.error'
  ) {
    return 'payment-failed'
  }
  if (event.name === 'checkout.closed') {
    return 'closed'
  }
  return undefined
}

function paddleFromWindow(window: Window): PaddleBrowser | undefined {
  return (window as Window & { Paddle?: PaddleBrowser }).Paddle
}

export async function loadPaddle(
  window: Window,
  document: Document,
): Promise<PaddleBrowser> {
  const existing = paddleFromWindow(window)
  if (existing) {
    return existing
  }
  const scriptId = 'postqron-paddle-js'
  let script = document.getElementById(scriptId) as HTMLScriptElement | null
  if (!script) {
    script = document.createElement('script')
    script.id = scriptId
    script.src = PADDLE_SCRIPT_URL
    script.async = true
    script.crossOrigin = 'anonymous'
    document.head.append(script)
  }
  await new Promise<void>((resolve, reject) => {
    const loaded = paddleFromWindow(window)
    if (loaded) {
      resolve()
      return
    }
    script!.addEventListener('load', () => resolve(), { once: true })
    script!.addEventListener(
      'error',
      () => reject(new Error('PADDLE_SCRIPT_UNAVAILABLE')),
      { once: true },
    )
  })
  const paddle = paddleFromWindow(window)
  if (!paddle) {
    throw new Error('PADDLE_SCRIPT_INVALID')
  }
  return paddle
}

let initializedToken: string | undefined

export function initializeAndOpenPaddle(
  paddle: PaddleBrowser,
  options: {
    eventCallback(event: PaddleEvent): void
    locale: string
    token: string
    transactionId: string
  },
): void {
  if (options.token.startsWith('test_')) {
    paddle.Environment.set('sandbox')
  }
  if (!initializedToken) {
    paddle.Initialize({
      token: options.token,
      checkout: {
        settings: {
          displayMode: 'overlay',
          theme: 'light',
          locale: options.locale,
        },
      },
      eventCallback: options.eventCallback,
    })
    initializedToken = options.token
  } else {
    if (initializedToken !== options.token) {
      throw new Error('PADDLE_TOKEN_CHANGED')
    }
    paddle.Update?.({ eventCallback: options.eventCallback })
  }
  paddle.Checkout.open({
    transactionId: options.transactionId,
    settings: {
      displayMode: 'overlay',
      theme: 'light',
      locale: options.locale,
    },
  })
}
