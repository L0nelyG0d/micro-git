import { Link } from "react-router-dom"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { formatDate } from "@/lib/format"
import type { Repo } from "@/lib/repos"

export function RepoTable({ repos }: { repos: Repo[] }) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Repository</TableHead>
          <TableHead>Description</TableHead>
          <TableHead>Created</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {repos.map((r) => (
          <TableRow key={r.id}>
            <TableCell className="max-w-48 break-all whitespace-normal">
              <Link to={`/${r.owner}/${r.name}`} className="font-medium text-primary hover:underline">
                {r.owner}/{r.name}
              </Link>
            </TableCell>
            <TableCell className="max-w-64 break-words whitespace-normal text-muted-foreground">
              {r.description || ""}
            </TableCell>
            <TableCell className="text-xs whitespace-nowrap text-muted-foreground">
              {formatDate(r.created_at)}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
