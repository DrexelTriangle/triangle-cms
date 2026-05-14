import { useState } from "react"
import { Pencil, Trash2 } from "lucide-react"

const INITIAL_CATEGORIES = [
  { id: 1, name: "News", slug: "news", description: "Campus and local news coverage", articles: 1247 },
  { id: 2, name: "Opinion", slug: "opinion", description: "Opinion pieces and editorials", articles: 432 },
  { id: 3, name: "Sports", slug: "sports", description: "Drexel Dragons sports coverage", articles: 891 },
  { id: 4, name: "Arts & Entertainment", slug: "arts-entertainment", description: "Arts, culture and entertainment", articles: 563 },
  { id: 5, name: "Campus", slug: "campus", description: "On-campus news and events", articles: 312 },
  { id: 6, name: "City", slug: "city", description: "Philadelphia city news", articles: 284 },
  { id: 7, name: "National", slug: "national", description: "National news affecting students", articles: 198 },
  { id: 8, name: "World", slug: "world", description: "International news and affairs", articles: 87 },
  { id: 9, name: "Comics", slug: "comics", description: "Student-created comics and art", articles: 124 },
]

function toSlug(name: string) {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "")
}

function CategoriesView() {
  const [categories, setCategories] = useState(INITIAL_CATEGORIES)
  const [name, setName] = useState("")
  const [slug, setSlug] = useState("")
  const [description, setDescription] = useState("")

  const handleNameChange = (val: string) => {
    setName(val)
    setSlug(toSlug(val))
  }

  const handleAdd = () => {
    if (!name.trim()) return
    setCategories((prev) => [
      ...prev,
      { id: Date.now(), name: name.trim(), slug: slug || toSlug(name), description: description.trim(), articles: 0 },
    ])
    setName("")
    setSlug("")
    setDescription("")
  }

  const handleDelete = (id: number) => setCategories((prev) => prev.filter((c) => c.id !== id))

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold text-foreground">Categories</h1>
        <span className="text-sm text-muted-foreground">{categories.length} categories</span>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-[320px_1fr] gap-6 items-start">
        {/* Add form */}
        <div className="rounded-xl border border-border bg-card p-5 flex flex-col gap-4">
          <h2 className="text-base font-semibold text-foreground">Add New Category</h2>

          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Name</label>
            <input
              className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
              placeholder="Category name"
              value={name}
              onChange={(e) => handleNameChange(e.target.value)}
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Slug</label>
            <input
              className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition font-mono"
              placeholder="auto-generated"
              value={slug}
              onChange={(e) => setSlug(e.target.value)}
            />
            <p className="text-[11px] text-muted-foreground">Auto-generated from name. Can be edited.</p>
          </div>

          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Description</label>
            <textarea
              className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition resize-none"
              rows={3}
              placeholder="Optional description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          </div>

          <button
            className="inline-flex items-center justify-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors"
            type="button"
            onClick={handleAdd}
          >
            Add Category
          </button>
        </div>

        {/* Table */}
        <div className="rounded-xl border border-border bg-card overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border bg-muted/40">
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Name</th>
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Slug</th>
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground hidden md:table-cell" scope="col">Description</th>
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Articles</th>
                <th className="text-right px-4 py-3 font-semibold text-muted-foreground" scope="col">Actions</th>
              </tr>
            </thead>
            <tbody>
              {categories.map((cat) => (
                <tr key={cat.id} className="border-b border-border last:border-0 hover:bg-muted/30 transition-colors">
                  <td className="px-4 py-3 font-medium text-foreground">{cat.name}</td>
                  <td className="px-4 py-3 text-muted-foreground font-mono text-xs">{cat.slug}</td>
                  <td className="px-4 py-3 text-muted-foreground hidden md:table-cell max-w-[200px] truncate">{cat.description || "—"}</td>
                  <td className="px-4 py-3">
                    <span className="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-muted text-muted-foreground">
                      {cat.articles.toLocaleString()}
                    </span>
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex items-center justify-end gap-1">
                      <button className="p-1.5 rounded-lg text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors" type="button" title="Edit">
                        <Pencil className="w-4 h-4" />
                      </button>
                      <button className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors" type="button" title="Delete" onClick={() => handleDelete(cat.id)}>
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}

export default CategoriesView
