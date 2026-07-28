export type LegalInline =
  | { type: 'text', value: string }
  | { type: 'strong' | 'emphasis', children: LegalInline[] }
  | { type: 'code', value: string }
  | { type: 'link', href: string, children: LegalInline[], external: boolean }

export type LegalBlock =
  | { type: 'heading', level: 1 | 2 | 3, children: LegalInline[] }
  | { type: 'paragraph', children: LegalInline[] }
  | { type: 'list', ordered: boolean, items: LegalInline[][] }
  | { type: 'table', header: LegalInline[][], rows: LegalInline[][][] }

const BLOCK_START = /^(?:#{1,3}\s+|[-*+]\s+|\d+[.)]\s+)/u
const TABLE_DIVIDER = /^\s*\|?(?:\s*:?-{3,}:?\s*\|)+\s*:?-{3,}:?\s*\|?\s*$/u
const SAFE_PROTOCOLS = new Set(['http:', 'https:', 'mailto:'])
const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/u

export function safeLegalHref(value: string): string | null {
  const candidate = value.trim()
  if (EMAIL.test(candidate)) {
    return `mailto:${candidate}`
  }
  try {
    const url = new URL(candidate)
    return SAFE_PROTOCOLS.has(url.protocol) ? url.href : null
  }
  catch {
    return null
  }
}

function text(value: string): LegalInline[] {
  return value ? [{ type: 'text', value }] : []
}

function nextInlineToken(source: string): {
  index: number
  length: number
  nodes: LegalInline[]
} | null {
  const candidates: Array<{
    index: number
    length: number
    nodes: LegalInline[]
  }> = []
  const code = /`([^`\n]+)`/u.exec(source)
  if (code?.index !== undefined && code[1] !== undefined) {
    candidates.push({
      index: code.index,
      length: code[0].length,
      nodes: [{ type: 'code', value: code[1] }],
    })
  }
  const link = /\[([^\]\n]+)\]\(([^)\s]+)\)/u.exec(source)
  if (link?.index !== undefined && link[1] !== undefined && link[2] !== undefined) {
    const href = safeLegalHref(link[2])
    candidates.push({
      index: link.index,
      length: link[0].length,
      nodes: href
        ? [{
            type: 'link',
            href,
            children: parseLegalInline(link[1]),
            external: href.startsWith('http:') || href.startsWith('https:'),
          }]
        : text(link[0]),
    })
  }
  const strong = /\*\*([^*\n]+)\*\*/u.exec(source)
  if (strong?.index !== undefined && strong[1] !== undefined) {
    candidates.push({
      index: strong.index,
      length: strong[0].length,
      nodes: [{ type: 'strong', children: parseLegalInline(strong[1]) }],
    })
  }
  const emphasis = /(?<!\*)\*([^*\n]+)\*(?!\*)/u.exec(source)
  if (emphasis?.index !== undefined && emphasis[1] !== undefined) {
    candidates.push({
      index: emphasis.index,
      length: emphasis[0].length,
      nodes: [{ type: 'emphasis', children: parseLegalInline(emphasis[1]) }],
    })
  }
  const automatic = /(?:https?:\/\/[^\s<]+|[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,})/iu.exec(source)
  if (automatic?.index !== undefined) {
    const raw = automatic[0]
    const value = raw.replace(/[),.;:!?]+$/u, '')
    const href = safeLegalHref(value)
    if (href) {
      const trailing = raw.slice(value.length)
      candidates.push({
        index: automatic.index,
        length: raw.length,
        nodes: [
          {
            type: 'link',
            href,
            children: text(value),
            external: href.startsWith('http:') || href.startsWith('https:'),
          },
          ...text(trailing),
        ],
      })
    }
  }
  return candidates.sort((left, right) =>
    left.index - right.index || left.length - right.length)[0] || null
}

export function parseLegalInline(source: string): LegalInline[] {
  const nodes: LegalInline[] = []
  let remaining = source
  while (remaining) {
    const token = nextInlineToken(remaining)
    if (!token) {
      nodes.push(...text(remaining))
      break
    }
    nodes.push(...text(remaining.slice(0, token.index)), ...token.nodes)
    remaining = remaining.slice(token.index + token.length)
  }
  return nodes
}

function tableCells(line: string): LegalInline[][] {
  const trimmed = line.trim().replace(/^\|/u, '').replace(/\|$/u, '')
  return trimmed.split('|').map(cell => parseLegalInline(cell.trim()))
}

function startsBlock(lines: string[], index: number): boolean {
  const line = lines[index] || ''
  const next = lines[index + 1] || ''
  return BLOCK_START.test(line) || (line.includes('|') && TABLE_DIVIDER.test(next))
}

export function parseLegalMarkdown(markdown: string): LegalBlock[] {
  const lines = markdown.replace(/\r\n?/gu, '\n').split('\n')
  const blocks: LegalBlock[] = []
  let index = 0
  while (index < lines.length) {
    const line = lines[index] || ''
    if (!line.trim()) {
      index += 1
      continue
    }

    const heading = /^(#{1,3})\s+(.+?)\s*#*\s*$/u.exec(line)
    if (heading?.[1] && heading[2]) {
      blocks.push({
        type: 'heading',
        level: heading[1].length as 1 | 2 | 3,
        children: parseLegalInline(heading[2]),
      })
      index += 1
      continue
    }

    if (line.includes('|') && TABLE_DIVIDER.test(lines[index + 1] || '')) {
      const header = tableCells(line)
      const rows: LegalInline[][][] = []
      index += 2
      while (index < lines.length && (lines[index] || '').includes('|')) {
        rows.push(tableCells(lines[index] || ''))
        index += 1
      }
      blocks.push({ type: 'table', header, rows })
      continue
    }

    const listItem = /^(\s*)([-*+]|\d+[.)])\s+(.+)$/u.exec(line)
    if (listItem?.[2] && listItem[3]) {
      const ordered = /^\d/u.test(listItem[2])
      const items: LegalInline[][] = []
      while (index < lines.length) {
        const item = /^(\s*)([-*+]|\d+[.)])\s+(.+)$/u.exec(lines[index] || '')
        if (!item?.[2] || !item[3] || /^\d/u.test(item[2]) !== ordered) {
          break
        }
        items.push(parseLegalInline(item[3]))
        index += 1
      }
      blocks.push({ type: 'list', ordered, items })
      continue
    }

    const paragraph: string[] = [line.trim()]
    index += 1
    while (index < lines.length && (lines[index] || '').trim() && !startsBlock(lines, index)) {
      paragraph.push((lines[index] || '').trim())
      index += 1
    }
    blocks.push({
      type: 'paragraph',
      children: parseLegalInline(paragraph.join(' ')),
    })
  }
  return blocks
}
