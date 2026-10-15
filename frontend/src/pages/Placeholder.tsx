import { useParams } from "react-router-dom"

export function Placeholder({ name }: { name: string }) {
  const params = useParams()
  return (
    <div className="flex flex-col gap-3">
      <h1 className="text-xl font-semibold">{name}</h1>
      <dl className="text-sm text-muted-foreground">
        {Object.entries(params).map(([k, v]) => (
          <div key={k} className="flex gap-2">
            <dt>{k}:</dt>
            <dd className="font-mono">{v}</dd>
          </div>
        ))}
      </dl>
    </div>
  )
}
