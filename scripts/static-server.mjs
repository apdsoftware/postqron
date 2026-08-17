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
// Con --compress emula anche la compressione dell'edge: è la modalità da usare
// per misurare R53-bis in locale. Senza, Lighthouse conta i byte non compressi e
// segnala `uses-text-compression` come un difetto del sito, mentre è solo
// un'assenza del server di prova — il punteggio che ne esce sottostima quello
// vero di parecchi punti. La compressione resta però spenta di default: i test
// e2e misurano il peso degli artefatti, e un peso compresso dipenderebbe dal
// livello di zlib invece che da ciò che la build produce.
//
// Nessuna dipendenza: è codice di test, non deve aggiungere superficie al
// progetto.
//
// Uso: node scripts/static-server.mjs --root <dir> --port <n> [--spa] [--compress]

import { createServer } from 'node:http'
import { createReadStream } from 'node:fs'
import { stat } from 'node:fs/promises'
import { extname, join, normalize, resolve, sep } from 'node:path'
import { createBrotliCompress, createGzip, constants as zlibConstants } from 'node:zlib'

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
  '.xml': 'application/xml; charset=utf-8',
}

// Le estensioni che Cloudflare comprime. Le altre — immagini, woff2 — sono già
// compresse nel loro formato: ricomprimerle costerebbe CPU per crescere di
// qualche byte, e infatti l'edge non lo fa.
const COMPRESSIBLE = new Set(['.html', '.js', '.mjs', '.css', '.json', '.svg', '.txt', '.map', '.xml'])

/**
 * @param {string[]} argv
 * @returns {{ root: string, port: number, spa: boolean, compress: boolean }}
 */
function parseArgs(argv) {
  let root = ''
  let port = -1
  let spa = false
  let compress = false

  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i]
    if (arg === '--root') root = argv[++i] ?? ''
    else if (arg === '--port') port = Number(argv[++i] ?? '')
    else if (arg === '--spa') spa = true
    else if (arg === '--compress') compress = true
    else {
      console.error(`static-server: argomento sconosciuto "${arg}"`)
      process.exit(2)
    }
  }

  // `--port 0` chiede una porta effimera: la usa la suite di test degli script,
  // che non può permettersi di collidere con qualcosa già in ascolto.
  if (!root || !Number.isInteger(port) || port < 0) {
    console.error('uso: node scripts/static-server.mjs --root <dir> --port <n> [--spa] [--compress]')
    process.exit(2)
  }

  return { root: resolve(root), port, spa, compress }
}

/**
 * Sceglie la codifica come farebbe l'edge: brotli se il client lo dichiara,
 * altrimenti gzip, altrimenti niente.
 *
 * @param {string | undefined} acceptEncoding
 * @param {string} extension
 * @returns {{ encoding: string, stream: import('node:stream').Transform } | null}
 */
function negotiate(acceptEncoding, extension) {
  if (!COMPRESSIBLE.has(extension)) return null

  // `split(';')[0]` è sempre definito, ma il typecheck degli e2e gira con
  // `noUncheckedIndexedAccess`: `?? value` dice la stessa cosa senza asserzioni,
  // che in un `.mjs` non si possono scrivere.
  const accepted = (acceptEncoding ?? '').split(',').map(value => (value.split(';')[0] ?? value).trim())
  if (accepted.includes('br')) {
    // Livello 5, non 11: è il compromesso che Cloudflare usa sul traffico
    // dinamico. Misurare con 11 gonfierebbe il risparmio rispetto al vero.
    return {
      encoding: 'br',
      stream: createBrotliCompress({ params: { [zlibConstants.BROTLI_PARAM_QUALITY]: 5 } }),
    }
  }
  if (accepted.includes('gzip')) return { encoding: 'gzip', stream: createGzip() }
  return null
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

const { root, port, spa, compress } = parseArgs(process.argv.slice(2))

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

    const extension = extname(file)
    const encoded = compress ? negotiate(req.headers['accept-encoding'], extension) : null

    /** @type {Record<string, string>} */
    const headers = {
      'content-type': CONTENT_TYPES[extension] ?? 'application/octet-stream',
      'cache-control': 'no-store',
    }
    if (compress && COMPRESSIBLE.has(extension)) headers.vary = 'accept-encoding'
    if (encoded) headers['content-encoding'] = encoded.encoding

    res.writeHead(status, headers)

    if (encoded) createReadStream(file).pipe(encoded.stream).pipe(res)
    else createReadStream(file).pipe(res)
  })()
})

server.listen(port, '127.0.0.1', () => {
  const address = server.address()
  const bound = typeof address === 'object' && address !== null ? address.port : port
  const modes = [spa ? 'spa' : '', compress ? 'compress' : ''].filter(Boolean).join(', ')
  console.log(`static-server: ${root} su http://127.0.0.1:${bound}${modes ? ` (${modes})` : ''}`)
})
