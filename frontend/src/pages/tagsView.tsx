import { useState } from "react"
import { Pencil, Trash2 } from "lucide-react"

const INITIAL_TAGS = [
  { id: 1, name: "drexel", slug: "drexel", description: "", articles: 4832 },
  { id: 2, name: "philadelphia", slug: "philadelphia", description: "", articles: 2104 },
  { id: 3, name: "campus", slug: "campus", description: "", articles: 1876 },
  { id: 4, name: "students", slug: "students", description: "", articles: 1543 },
  { id: 5, name: "academics", slug: "academics", description: "", articles: 987 },
  { id: 6, name: "sports", slug: "sports", description: "", articles: 876 },
  { id: 7, name: "community", slug: "community", description: "", articles: 743 },
  { id: 8, name: "health", slug: "health", description: "", articles: 621 },
  { id: 9, name: "technology", slug: "technology", description: "", articles: 534 },
  { id: 10, name: "arts", slug: "arts", description: "", articles: 498 },
  { id: 11, name: "politics", slug: "politics", description: "", articles: 412 },
  { id: 12, name: "environment", slug: "environment", description: "", articles: 287 },
  { id: 13, name: "international", slug: "international", description: "", articles: 234 },
  { id: 14, name: "alumni", slug: "alumni", description: "", articles: 198 },
  { id: 15, name: "research", slug: "research", description: "", articles: 176 },
]

function toSlug(name: string) {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "")
}

function TagsView() {
  const [tags, setTags] = useState(INITIAL_TAGS)
  const [name, setName] = useState("")
  const [slug, setSlug] = useState("")
  const [description, setDescription] = useState("")

  const handleNameChange = (val: string) => {
    setName(val)
    setSlug(toSlug(val))
  }

  const handleAdd = () => {
    if (!name.trim()) return
    setTags((prev) => [
      ...prev,
      { id: Date.now(), name: name.trim(), slug: slug || toSlug(name), description: description.trim(), articles: 0 },
    ])
    setName("")
    setSlug("")
    setDescription("")
  }

  const handleDelete = (id: number) => setTags((prev) => prev.filter((t) => t.id !== id))

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold text-foreground">Tags</h1>
        <span className="text-sm text-muted-foreground">{tags.length} tags</span>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-[320px_1fr] gap-6 items-start">
        {/* Add form */}
        <div className="rounded-xl border border-border bg-card p-5 flex flex-col gap-4">
          <h2 className="text-base font-semibold text-foreground">Add New Tag</h2>

          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Name</label>
            <input
              className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
              placeholder="Tag name"
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
            Add Tag
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
              {tags.map((tag) => (
                <tr key={tag.id} className="border-b border-border last:border-0 hover:bg-muted/30 transition-colors">
                  <td className="px-4 py-3 font-medium text-foreground">{tag.name}</td>
                  <td className="px-4 py-3 text-muted-foreground font-mono text-xs">{tag.slug}</td>
                  <td className="px-4 py-3 text-muted-foreground hidden md:table-cell">{tag.description || "—"}</td>
                  <td className="px-4 py-3">
                    <span className="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-primary/10 text-primary">
                      {tag.articles.toLocaleString()}
                    </span>
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex items-center justify-end gap-1">
                      <button className="p-1.5 rounded-lg text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors" type="button" title="Edit">
                        <Pencil className="w-4 h-4" />
                      </button>
                      <button className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors" type="button" title="Delete" onClick={() => handleDelete(tag.id)}>
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

export default TagsView
