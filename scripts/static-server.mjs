// Server statico minimo per i test end-to-end.
//
// I frontend di Postqron sono distribuiti come file statici su Cloudflare Pages
// (SPEC §2): in produzione nessun processo Node serve quelle pagine. I test e2e
// devono quindi partire da `apps/*/.output/public` così com'è, non da `nuxt
// preview` — che avvia Nitro e proverebbe una cosa che in produzione non esiste.
//
// Questo file emula il comportamento di Cloudflare Pages per quel che serve ai
// test: file statici, index.html sulle directory e, con --spa, il fallback su
// index.html per i percorsi che non corrispondono a un file (la regola di
// apps/dashboard/public/_redirects).
//
// Nessuna dipendenza: è codice di test, non deve aggiungere superficie al
// progetto.
//
// Uso: node scripts/static-server.mjs --root <dir> --port <n> [--spa]

import { createServer } from 'node:http'
import { createReadStream } from 'node:fs'
import { stat } from 'node:fs/promises'
import { extname, join, normalize, resolve, sep } from 'node:path'

/** @type {Record<string, string>} */
const CONTENT_TYPES = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.webp': 'image/webp',
  '.ico': 'image/x-icon',
  '.woff2': 'font/woff2',
  '.txt': 'text/plain; charset=utf-8',
  '.map': 'application/json; charset=utf-8',
}

/**
 * @param {string[]} argv
 * @returns {{ root: string, port: number, spa: boolean }}
 */
function parseArgs(argv) {
  let root = ''
  let port = -1
  let spa = false

  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i]
    if (arg === '--root') root = argv[++i] ?? ''
    else if (arg === '--port') port = Number(argv[++i] ?? '')
    else if (arg === '--spa') spa = true
    else {
      console.error(`static-server: argomento sconosciuto "${arg}"`)
      process.exit(2)
    }
  }

  // `--port 0` chiede una porta effimera: la usa la suite di test degli script,
  // che non può permettersi di collidere con qualcosa già in ascolto.
  if (!root || !Number.isInteger(port) || port < 0) {
    console.error('uso: node scripts/static-server.mjs --root <dir> --port <n> [--spa]')
    process.exit(2)
  }

  return { root: resolve(root), port, spa }
}

/**
 * Risolve un percorso HTTP in un file dentro root, rifiutando le uscite dalla
 * radice (`..`, percorsi codificati) prima ancora di toccare il filesystem.
 *
 * @param {string} root
 * @param {string} pathname
 * @returns {string | null}
 */
function resolveWithinRoot(root, pathname) {
  let decoded
  try {
    decoded = decodeURIComponent(pathname)
  }
  catch {
    return null
  }

  const candidate = resolve(join(root, normalize(decoded)))
  if (candidate !== root && !candidate.startsWith(root + sep)) return null
  return candidate
}

/**
 * @param {string} path
 * @returns {Promise<string | null>} il file da servire, o null se non esiste
 */
async function findFile(path) {
  try {
    const info = await stat(path)
    if (info.isDirectory()) return findFile(join(path, 'index.html'))
    return info.isFile() ? path : null
  }
  catch {
    return null
  }
}

const { root, port, spa } = parseArgs(process.argv.slice(2))

const server = createServer((req, res) => {
  void (async () => {
    const url = new URL(req.url ?? '/', 'http://localhost')
    const target = resolveWithinRoot(root, url.pathname)

    let file = target === null ? null : await findFile(target)
    let status = 200

    if (file === null && spa) {
      // Regola `/* /index.html 200` di Cloudflare Pages: il router Vue risolve
      // la rotta lato client. Lo status resta 200, come sull'edge.
      file = await findFile(join(root, 'index.html'))
    }

    if (file === null) {
      status = 404
      file = await findFile(join(root, '404.html'))
    }

    if (file === null) {
      res.writeHead(404, { 'content-type': 'text/plain; charset=utf-8' })
      res.end('404\n')
      return
    }

    res.writeHead(status, {
      'content-type': CONTENT_TYPES[extname(file)] ?? 'application/octet-stream',
      'cache-control': 'no-store',
    })
    createReadStream(file).pipe(res)
  })()
})

server.listen(port, '127.0.0.1', () => {
  const address = server.address()
  const bound = typeof address === 'object' && address !== null ? address.port : port
  console.log(`static-server: ${root} su http://127.0.0.1:${bound}${spa ? ' (spa)' : ''}`)
})
