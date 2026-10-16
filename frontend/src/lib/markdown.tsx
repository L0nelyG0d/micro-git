import type { ReactNode } from "react"

/**
 * Tiny markdown renderer that builds React elements only (no HTML strings).
 * Supported: # headings, paragraphs, -/* and numbered lists, fenced code,
 * `code`, **bold**, *italic*. Everything else stays plain text.
 */

const INLINE = /`([^`]+)`|\*\*([^*]+)\*\*|\*([^*\s](?:[^*]*[^*\s])?)\*/g
const HEADING = /^(#{1,6}) +(.*)$/
const BULLET = /^[-*] +(.*)$/
const NUMBERED = /^\d+\. +(.*)$/
const FENCE = /^```/

const headingClass: Record<number, string> = {
  1: "text-xl font-semibold",
  2: "text-lg font-semibold",
  3: "text-base font-semibold",
  4: "text-sm font-semibold",
  5: "text-sm font-semibold",
  6: "text-sm font-semibold text-muted-foreground",
}

function inline(text: string): ReactNode[] {
  const out: ReactNode[] = []
  let last = 0
  for (const m of text.matchAll(INLINE)) {
    const at = m.index ?? 0
    if (at > last) out.push(text.slice(last, at))
    if (m[1] !== undefined) {
      out.push(
        <code key={at} className="rounded bg-muted px-1 font-mono text-sm">
          {m[1]}
        </code>,
      )
    } else if (m[2] !== undefined) {
      out.push(<strong key={at}>{m[2]}</strong>)
    } else {
      out.push(<em key={at}>{m[3]}</em>)
    }
    last = at + m[0].length
  }
  if (last < text.length) out.push(text.slice(last))
  return out
}

function isBlockStart(line: string): boolean {
  return FENCE.test(line) || HEADING.test(line) || BULLET.test(line) || NUMBERED.test(line)
}

export function renderMarkdown(source: string): ReactNode[] {
  const lines = source.replace(/\r\n?/g, "\n").split("\n")
  const blocks: ReactNode[] = []
  let i = 0
  while (i < lines.length) {
    const line = lines[i]
    const key = blocks.length
    if (line.trim() === "") {
      i++
    } else if (FENCE.test(line)) {
      const code: string[] = []
      i++
      while (i < lines.length && !FENCE.test(lines[i])) code.push(lines[i++])
      i++ // closing fence
      blocks.push(
        <pre key={key} className="overflow-x-auto rounded-md bg-muted p-3 font-mono text-sm">
          <code>{code.join("\n")}</code>
        </pre>,
      )
    } else if (HEADING.test(line)) {
      const [, hashes, text] = line.match(HEADING)!
      const Tag = `h${hashes.length}` as "h1"
      blocks.push(
        <Tag key={key} className={headingClass[hashes.length]}>
          {inline(text)}
        </Tag>,
      )
      i++
    } else if (BULLET.test(line) || NUMBERED.test(line)) {
      const re = BULLET.test(line) ? BULLET : NUMBERED
      const items: ReactNode[] = []
      while (i < lines.length && re.test(lines[i])) {
        items.push(<li key={items.length}>{inline(lines[i].match(re)![1])}</li>)
        i++
      }
      blocks.push(
        re === BULLET ? (
          <ul key={key} className="list-disc pl-5">
            {items}
          </ul>
        ) : (
          <ol key={key} className="list-decimal pl-5">
            {items}
          </ol>
        ),
      )
    } else {
      const para: string[] = []
      while (i < lines.length && lines[i].trim() !== "" && (para.length === 0 || !isBlockStart(lines[i]))) {
        para.push(lines[i++])
      }
      blocks.push(
        <p key={key} className="break-words">
          {inline(para.join("\n"))}
        </p>,
      )
    }
  }
  return blocks
}
