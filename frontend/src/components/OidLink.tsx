import { Check, Copy } from "lucide-react"
import { useState } from "react"
import { Link } from "react-router-dom"
import { Button } from "@/components/ui/button"
import { shortOid } from "@/lib/format"

/** 7-char oid in monospace, linked with the full oid; full oid in title. */
export function OidLink({ oid, to, copyable }: { oid: string; to: string; copyable?: boolean }) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(oid)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      /* clipboard unavailable: the full oid is still in the title */
    }
  }
  return (
    <span className="inline-flex items-center gap-1">
      <Link to={to} title={oid} className="font-mono text-sm text-primary hover:underline">
        {shortOid(oid)}
      </Link>
      {copyable && (
        <Button variant="ghost" size="icon-xs" aria-label="Copy full commit id" onClick={copy}>
          {copied ? <Check /> : <Copy />}
        </Button>
      )}
    </span>
  )
}
