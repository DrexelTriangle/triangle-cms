import { useState } from "react"
import { Eye, Edit, Trash2, Plus, FileText, ExternalLink } from "lucide-react"

type Page = {
  id: number
  title: string
  slug: string
  status: "published" | "draft" | "private"
  author: string
  lastModified: string
  template: string
}

const dummy: Page[] = [
  { id: 1, title: "About Us", slug: "about-us", status: "published", author: "Admin", lastModified: "May 1, 2026", template: "Default" },
  { id: 2, title: "Contact", slug: "contact", status: "published", author: "Admin", lastModified: "Apr 28, 2026", template: "Contact" },
  { id: 3, title: "Advertise", slug: "advertise", status: "published", author: "Admin", lastModified: "Apr 20, 2026", template: "Default" },
  { id: 4, title: "Staff", slug: "staff", status: "published", author: "Admin", lastModified: "Apr 15, 2026", template: "Staff" },
  { id: 5, title: "Privacy Policy", slug: "privacy-policy", status: "published", author: "Admin", lastModified: "Mar 10, 2026", template: "Default" },
  { id: 6, title: "Terms of Service", slug: "terms-of-service", status: "published", author: "Admin", lastModified: "Mar 10, 2026", template: "Default" },
  { id: 7, title: "Submit a Tip", slug: "submit-tip", status: "draft", author: "Admin", lastModified: "May 3, 2026", template: "Default" },
  { id: 8, title: "Archive", slug: "archive", status: "private", author: "Admin", lastModified: "Jan 5, 2026", template: "Archive" },
]

const STATUS_STYLES: Record<Page["status"], string> = {
  published: "bg-green-100 text-green-700",
  draft: "bg-yellow-100 text-yellow-700",
  private: "bg-gray-100 text-gray-600",
}

export default function PagesView() {
  const [pages, setPages] = useState(dummy)
  const [search, setSearch] = useState("")

  const filtered = pages.filter((p) => p.title.toLowerCase().includes(search.toLowerCase()))

  return (
    <div className="p-6 max-w-5xl mx-auto">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-xl font-bold text-foreground">Pages</h1>
          <p className="text-sm text-muted-foreground mt-0.5">{pages.length} static pages</p>
        </div>
        <button className="flex items-center gap-2 px-4 py-2 bg-primary text-white rounded-lg text-sm font-medium hover:bg-primary/90 transition-colors">
          <Plus className="w-4 h-4" />
          Add New Page
        </button>
      </div>

      <div className="bg-card border border-border rounded-xl overflow-hidden">
        <div className="flex items-center gap-3 px-4 py-3 border-b border-border">
          <input
            type="text"
            placeholder="Search pages..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="flex-1 max-w-xs px-3 py-1.5 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/30"
          />
        </div>

        <table className="w-full text-sm">
          <thead className="bg-muted/40 text-muted-foreground text-xs uppercase tracking-wide">
            <tr>
              <th className="text-left px-4 py-3 font-medium">Title</th>
              <th className="text-left px-4 py-3 font-medium">Author</th>
              <th className="text-left px-4 py-3 font-medium">Template</th>
              <th className="text-left px-4 py-3 font-medium">Status</th>
              <th className="text-left px-4 py-3 font-medium">Last Modified</th>
              <th className="px-4 py-3" />
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {filtered.map((page) => (
              <tr key={page.id} className="hover:bg-muted/20 group">
                <td className="px-4 py-3">
                  <div className="flex items-center gap-2">
                    <FileText className="w-4 h-4 text-muted-foreground shrink-0" />
                    <span className="font-medium text-foreground">{page.title}</span>
                  </div>
                  <p className="text-xs text-muted-foreground mt-0.5 ml-6">/{page.slug}</p>
                </td>
                <td className="px-4 py-3 text-muted-foreground">{page.author}</td>
                <td className="px-4 py-3 text-muted-foreground">{page.template}</td>
                <td className="px-4 py-3">
                  <span className={`px-2 py-0.5 rounded-full text-xs font-medium capitalize ${STATUS_STYLES[page.status]}`}>
                    {page.status}
                  </span>
                </td>
                <td className="px-4 py-3 text-muted-foreground">{page.lastModified}</td>
                <td className="px-4 py-3">
                  <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity justify-end">
                    <button className="p-1.5 rounded-md hover:bg-muted text-muted-foreground hover:text-foreground transition-colors" title="View"><Eye className="w-3.5 h-3.5" /></button>
                    <button className="p-1.5 rounded-md hover:bg-muted text-muted-foreground hover:text-foreground transition-colors" title="Edit"><Edit className="w-3.5 h-3.5" /></button>
                    <button className="p-1.5 rounded-md hover:bg-muted text-muted-foreground hover:text-foreground transition-colors" title="Open"><ExternalLink className="w-3.5 h-3.5" /></button>
                    <button onClick={() => setPages(pages.filter((p) => p.id !== page.id))} className="p-1.5 rounded-md hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors" title="Delete"><Trash2 className="w-3.5 h-3.5" /></button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>

        {filtered.length === 0 && (
          <div className="py-16 text-center text-muted-foreground text-sm">No pages found.</div>
        )}
      </div>
    </div>
  )
}
