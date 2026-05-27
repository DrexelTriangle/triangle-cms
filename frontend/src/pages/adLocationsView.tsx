import { useState } from "react"
import { Pencil, Trash2, Plus, Eye, EyeOff } from "lucide-react"

type AdStatus = "active" | "inactive"

const LOCATIONS = [
  { id: 1, name: "Header Banner", slug: "header-banner", size: "728×90", description: "Leaderboard at the top of every page", status: "active" as AdStatus, impressions: 84200 },
  { id: 2, name: "Sidebar Rectangle", slug: "sidebar-rect", size: "300×250", description: "Medium rectangle in the article sidebar", status: "active" as AdStatus, impressions: 61800 },
  { id: 3, name: "In-Article (Mid)", slug: "in-article-mid", size: "336×280", description: "Large rectangle inserted after 3rd paragraph", status: "active" as AdStatus, impressions: 54300 },
  { id: 4, name: "Footer Banner", slug: "footer-banner", size: "728×90", description: "Leaderboard in the site footer", status: "active" as AdStatus, impressions: 38900 },
  { id: 5, name: "Mobile Sticky", slug: "mobile-sticky", size: "320×50", description: "Fixed banner at bottom on mobile devices", status: "active" as AdStatus, impressions: 47200 },
  { id: 6, name: "Homepage Takeover", slug: "homepage-takeover", size: "1200×628", description: "Full-width feature placement on homepage", status: "inactive" as AdStatus, impressions: 0 },
  { id: 7, name: "Newsletter Banner", slug: "newsletter-banner", size: "600×150", description: "Banner included in weekly newsletter emails", status: "active" as AdStatus, impressions: 19400 },
  { id: 8, name: "Section Header", slug: "section-header", size: "970×90", description: "Wide banner on section/category pages", status: "inactive" as AdStatus, impressions: 0 },
]

function toSlug(name: string) {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "")
}

export default function AdLocationsView() {
  const [locations, setLocations] = useState(LOCATIONS)
  const [name, setName] = useState("")
  const [slug, setSlug] = useState("")
  const [size, setSize] = useState("")
  const [description, setDescription] = useState("")

  const handleNameChange = (val: string) => {
    setName(val)
    setSlug(toSlug(val))
  }

  const handleAdd = () => {
    if (!name.trim()) return
    setLocations((prev) => [
      ...prev,
      { id: Date.now(), name: name.trim(), slug: slug || toSlug(name), size: size.trim() || "—", description: description.trim(), status: "inactive" as AdStatus, impressions: 0 },
    ])
    setName(""); setSlug(""); setSize(""); setDescription("")
  }

  const toggleStatus = (id: number) => setLocations((prev) =>
    prev.map((l) => l.id === id ? { ...l, status: (l.status === "active" ? "inactive" : "active") as AdStatus } : l)
  )

  const remove = (id: number) => setLocations((prev) => prev.filter((l) => l.id !== id))

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Ad Locations</h1>
          <p className="text-sm text-muted-foreground mt-0.5">{locations.filter((l) => l.status === "active").length} active placements</p>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-[320px_1fr] gap-6 items-start">
        {/* Add form */}
        <div className="rounded-xl border border-border bg-card p-5 flex flex-col gap-4">
          <h2 className="text-base font-semibold text-foreground flex items-center gap-2">
            <Plus className="w-4 h-4" />
            Add Ad Location
          </h2>

          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Name</label>
            <input
              className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
              placeholder="e.g. Sidebar Rectangle"
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
            <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Size</label>
            <input
              className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition font-mono"
              placeholder="e.g. 300×250"
              value={size}
              onChange={(e) => setSize(e.target.value)}
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Description</label>
            <textarea
              className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition resize-none"
              rows={3}
              placeholder="Where this ad appears"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          </div>

          <button
            className="inline-flex items-center justify-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors"
            type="button"
            onClick={handleAdd}
          >
            Add Location
          </button>
        </div>

        {/* Table */}
        <div className="rounded-xl border border-border bg-card overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border bg-muted/40">
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Name</th>
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Size</th>
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground hidden md:table-cell" scope="col">Description</th>
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Status</th>
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground hidden lg:table-cell" scope="col">Impressions</th>
                <th className="text-right px-4 py-3 font-semibold text-muted-foreground" scope="col">Actions</th>
              </tr>
            </thead>
            <tbody>
              {locations.map((loc) => (
                <tr key={loc.id} className="border-b border-border last:border-0 hover:bg-muted/30 transition-colors">
                  <td className="px-4 py-3">
                    <p className="font-medium text-foreground">{loc.name}</p>
                    <p className="text-[11px] font-mono text-muted-foreground">{loc.slug}</p>
                  </td>
                  <td className="px-4 py-3 font-mono text-xs text-muted-foreground">{loc.size}</td>
                  <td className="px-4 py-3 text-muted-foreground hidden md:table-cell max-w-[180px] truncate">{loc.description || "—"}</td>
                  <td className="px-4 py-3">
                    <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium ${loc.status === "active" ? "bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400" : "bg-muted text-muted-foreground"}`}>
                      {loc.status}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-muted-foreground hidden lg:table-cell">
                    {loc.impressions > 0 ? loc.impressions.toLocaleString() : "—"}
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex items-center justify-end gap-1">
                      <button
                        onClick={() => toggleStatus(loc.id)}
                        className={`p-1.5 rounded-lg transition-colors ${loc.status === "active" ? "text-muted-foreground hover:text-orange-500 hover:bg-orange-50 dark:hover:bg-orange-900/20" : "text-muted-foreground hover:text-green-600 hover:bg-green-50 dark:hover:bg-green-900/20"}`}
                        type="button"
                        title={loc.status === "active" ? "Deactivate" : "Activate"}
                      >
                        {loc.status === "active" ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
                      </button>
                      <button className="p-1.5 rounded-lg text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors" type="button" title="Edit">
                        <Pencil className="w-4 h-4" />
                      </button>
                      <button onClick={() => remove(loc.id)} className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors" type="button" title="Delete">
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
