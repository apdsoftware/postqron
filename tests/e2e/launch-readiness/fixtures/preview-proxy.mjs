import { createServer, request as forwardRequest } from 'node:http'
import process from 'node:process'

const host = '127.0.0.1'
const port = Number(process.env.LAUNCH_PROXY_PORT)
const webPort = Number(process.env.LAUNCH_WEB_PORT)
const fixturePort = Number(process.env.LAUNCH_FIXTURE_PORT || 41797)
const supervisorPid = Number(process.env.LAUNCH_SUPERVISOR_PID)

if (!port || !webPort) {
  throw new Error('LAUNCH_PROXY_PORT and LAUNCH_WEB_PORT are required')
}

const server = createServer((incoming, outgoing) => {
  const useFixture = incoming.url?.startsWith('/api/v1/')
    || incoming.url?.startsWith('/__fixture/')
  const targetPort = useFixture ? fixturePort : webPort
  const headers = {
    ...incoming.headers,
    host: `${host}:${targetPort}`,
  }
  const forwarded = forwardRequest({
    host,
    port: targetPort,
    method: incoming.method,
    path: incoming.url,
    headers,
  }, response => {
    outgoing.writeHead(response.statusCode || 502, response.headers)
    response.pipe(outgoing)
  })
  forwarded.on('error', error => {
    if (!outgoing.headersSent) {
      outgoing.writeHead(502, { 'content-type': 'text/plain; charset=utf-8' })
    }
    outgoing.end(`launch preview proxy failed: ${error.message}\n`)
  })
  incoming.pipe(forwarded)
})

server.listen(port, host, () => {
  process.stdout.write(`launch preview proxy listening on http://${host}:${port}\n`)
})

let stopping = false
function stop() {
  if (stopping) {
    return
  }
  stopping = true
  clearInterval(supervisorWatchdog)
  server.close(() => process.exit(0))
  setTimeout(() => process.exit(0), 2_000).unref()
}

const supervisorWatchdog = setInterval(() => {
  if (!supervisorPid) {
    return
  }
  try {
    process.kill(supervisorPid, 0)
  }
  catch {
    stop()
  }
}, 500)
supervisorWatchdog.unref()

process.once('SIGHUP', stop)
process.once('SIGINT', stop)
process.once('SIGTERM', stop)
