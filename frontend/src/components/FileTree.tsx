import { File, Folder } from "lucide-react"
import { Link } from "react-router-dom"
import { Table, TableBody, TableCell, TableRow } from "@/components/ui/table"
import { uiPath, type TreeEntry } from "@/lib/git"

/** Entries as returned by the API (directories first, then files). */
export function FileTree({
  owner,
  repo,
  ref_,
  path,
  entries,
}: {
  owner: string
  repo: string
  ref_: string
  path: string
  entries: TreeEntry[]
}) {
  return (
    <Table aria-label="Files">
      <TableBody>
        {entries.map((e) => {
          const full = path ? `${path}/${e.name}` : e.name
          const isDir = e.type === "tree"
          return (
            <TableRow key={e.name}>
              <TableCell>
                <Link
                  to={uiPath(isDir ? "tree" : "blob", owner, repo, ref_, full)}
                  className="inline-flex items-center gap-2 text-primary hover:underline"
                >
                  {isDir ? (
                    <Folder aria-hidden className="size-4 text-muted-foreground" />
                  ) : (
                    <File aria-hidden className="size-4 text-muted-foreground" />
                  )}
                  {e.name}
                </Link>
              </TableCell>
            </TableRow>
          )
        })}
      </TableBody>
    </Table>
  )
}
