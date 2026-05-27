import { useState } from "react"
import { Search, Plus, Pencil, Trash2 } from "lucide-react"

const SECTIONS = [
  { id: 1, name: "News", slug: "news", parent: null, articles: 1890 },
  { id: 2, name: "Opinion", slug: "opinion", parent: null, articles: 432 },
  { id: 3, name: "Sports", slug: "sports", parent: null, articles: 891 },
  { id: 4, name: "Arts & Entertainment", slug: "arts-entertainment", parent: null, articles: 563 },
  { id: 5, name: "Comics", slug: "comics", parent: null, articles: 124 },
  { id: 6, name: "Campus", slug: "campus", parent: "News", articles: 312 },
  { id: 7, name: "City", slug: "city", parent: "News", articles: 284 },
  { id: 8, name: "National", slug: "national", parent: "News", articles: 198 },
  { id: 9, name: "World", slug: "world", parent: "News", articles: 87 },
  { id: 10, name: "100 Year Anniversary", slug: "100-year-anniversary", parent: null, articles: 45 },
]

export default function SectionsView() {
  const [search, setSearch] = useState("")

  const parents = SECTIONS.filter((s) => s.parent === null)
  const childrenOf = (name: string) => SECTIONS.filter((s) => s.parent === name)

  const rows: { section: typeof SECTIONS[0]; isChild: boolean; isLast: boolean }[] = []
  for (const parent of parents) {
    const children = childrenOf(parent.name)
    rows.push({ section: parent, isChild: false, isLast: false })
    children.forEach((child, i) => {
      rows.push({ section: child, isChild: true, isLast: i === children.length - 1 })
    })
  }

  const filtered = rows.filter(({ section }) =>
    section.name.toLowerCase().includes(search.toLowerCase()) ||
    section.slug.toLowerCase().includes(search.toLowerCase()),
  )

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Sections</h1>
          <p className="text-sm text-muted-foreground mt-0.5">{SECTIONS.length} sections</p>
        </div>
        <button className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors" type="button">
          <Plus className="w-4 h-4" />
          Add Section
        </button>
      </div>

      <div className="relative">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
        <input className="w-full pl-9 pr-4 py-2 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition" placeholder="Search sections..." type="search" value={search} onChange={(e) => setSearch(e.target.value)} />
      </div>

      <div className="rounded-xl border border-border bg-card overflow-hidden">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-border bg-muted/40">
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground">Name</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground">Slug</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground">Articles</th>
              <th className="text-right px-4 py-3 font-semibold text-muted-foreground">Actions</th>
            </tr>
          </thead>
          <tbody>
            {filtered.map(({ section: s, isChild, isLast }) => (
              <tr key={s.id} className="border-b border-border last:border-0 hover:bg-muted/30 transition-colors">
                <td className="px-4 py-3 font-medium text-foreground">
                  {isChild ? (
                    <span className="flex items-start gap-1.5">
                      <span className="flex flex-col items-center w-4 shrink-0 mt-0.5">
                        <span className="w-px h-2 bg-border" />
                        <span className="w-3 h-px bg-border" />
                        {!isLast && <span className="w-px flex-1 bg-transparent" />}
                      </span>
                      <span className="text-muted-foreground">{s.name}</span>
                    </span>
                  ) : (
                    s.name
                  )}
                </td>
                <td className="px-4 py-3 text-muted-foreground font-mono text-xs">{s.slug}</td>
                <td className="px-4 py-3">
                  <span className="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-primary/10 text-primary">
                    {s.articles.toLocaleString()}
                  </span>
                </td>
                <td className="px-4 py-3">
                  <div className="flex items-center justify-end gap-1">
                    <button className="p-1.5 rounded-lg text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors" type="button"><Pencil className="w-4 h-4" /></button>
                    <button className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors" type="button"><Trash2 className="w-4 h-4" /></button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
