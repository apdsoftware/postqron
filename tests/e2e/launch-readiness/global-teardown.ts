async function globalTeardown() {
  if (process.env.LAUNCH_BASE_URL) {
    return
  }

  try {
    await fetch('http://127.0.0.1:41797/__fixture/shutdown', {
      method: 'POST',
      signal: AbortSignal.timeout(2_000),
    })
  }
  catch {
    // The fixture may already have stopped with the Playwright web server.
  }
}

export default globalTeardown
