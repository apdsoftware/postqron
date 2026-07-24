declare module 'h3' {
  interface H3EventContext {
    features?: Array<{
      id: string
      kind: string
      version: string
    }>
  }
}

export {}
