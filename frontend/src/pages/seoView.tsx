import { Search, Globe, TrendingUp, Link2, AlertCircle, CheckCircle } from "lucide-react"

const SEO_ISSUES = [
  { id: 1, type: "warning", article: "Opinion: The University Should Lower Tuition", issue: "Meta description missing" },
  { id: 2, type: "warning", article: "Dragon Baseball Wins Conference Championship", issue: "Meta description too short (43 chars)" },
  { id: 3, type: "error", article: "Developing Story: Construction on Market St", issue: "Duplicate title tag detected" },
  { id: 4, type: "warning", article: "New Student Center Breaks Ground", issue: "No featured image set (affects Open Graph)" },
  { id: 5, type: "error", article: "Arts & Entertainment Preview: Spring Gallery", issue: "Broken canonical URL" },
]

const TOP_PAGES = [
  { path: "/news/drexel-engineering-building-2025", title: "Drexel Announces New Engineering Building", views: 12483, impressions: 41200 },
  { path: "/sports/dragon-basketball-temple", title: "Dragon Basketball Falls to Temple 78–72", views: 8921, impressions: 29400 },
  { path: "/campus/new-health-center", title: "New Campus Health Center Opens Its Doors", views: 6734, impressions: 19800 },
  { path: "/co-op/nasa-goddard-spotlight", title: "Co-op Spotlight: Students at NASA Goddard", views: 5219, impressions: 14200 },
  { path: "/city/market-street-construction", title: "Market Street Construction: What Students Need to Know", views: 4831, impressions: 12900 },
]

const SOCIAL_PREVIEW = {
  title: "The Triangle | Drexel University's Independent Student Newspaper",
  description: "Award-winning independent student journalism at Drexel University since 1925.",
  image: "/og-default.jpg",
}

export default function SeoView() {
  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">SEO</h1>
          <p className="text-sm text-muted-foreground mt-0.5">Search engine optimization overview</p>
        </div>
      </div>

      {/* Stats row */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Issues Found</p>
          <p className="text-2xl font-bold text-destructive mt-1">{SEO_ISSUES.length}</p>
        </div>
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Indexed Pages</p>
          <p className="text-2xl font-bold text-foreground mt-1">3,821</p>
        </div>
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Avg Position</p>
          <p className="text-2xl font-bold text-foreground mt-1">14.2</p>
        </div>
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Monthly Impressions</p>
          <p className="text-2xl font-bold text-foreground mt-1">218k</p>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Issues */}
        <div className="flex flex-col gap-4">
          <h2 className="text-base font-semibold text-foreground flex items-center gap-2">
            <AlertCircle className="w-4 h-4 text-destructive" />
            SEO Issues
          </h2>
          <div className="flex flex-col gap-2">
            {SEO_ISSUES.map((issue) => (
              <div key={issue.id} className={`flex items-start gap-3 rounded-xl border p-4 ${issue.type === "error" ? "border-destructive/30 bg-destructive/5" : "border-yellow-300/50 bg-yellow-50/50 dark:border-yellow-700/30 dark:bg-yellow-900/10"}`}>
                {issue.type === "error" ? (
                  <AlertCircle className="w-4 h-4 text-destructive shrink-0 mt-0.5" />
                ) : (
                  <AlertCircle className="w-4 h-4 text-yellow-500 shrink-0 mt-0.5" />
                )}
                <div className="flex-1 min-w-0">
                  <p className="text-sm font-medium text-foreground truncate">{issue.article}</p>
                  <p className="text-xs text-muted-foreground mt-0.5">{issue.issue}</p>
                </div>
                <span className={`text-xs font-semibold px-2 py-0.5 rounded-full shrink-0 ${issue.type === "error" ? "bg-destructive/10 text-destructive" : "bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400"}`}>
                  {issue.type}
                </span>
              </div>
            ))}
            <div className="flex items-center gap-2 p-3 rounded-xl border border-green-300/50 bg-green-50/50 dark:border-green-700/30 dark:bg-green-900/10">
              <CheckCircle className="w-4 h-4 text-green-600 shrink-0" />
              <p className="text-sm text-muted-foreground">All other published articles have valid meta tags.</p>
            </div>
          </div>
        </div>

        {/* Top pages */}
        <div className="flex flex-col gap-4">
          <h2 className="text-base font-semibold text-foreground flex items-center gap-2">
            <TrendingUp className="w-4 h-4 text-primary" />
            Top Performing Pages
          </h2>
          <div className="rounded-xl border border-border bg-card overflow-hidden">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border bg-muted/40">
                  <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Page</th>
                  <th className="text-right px-4 py-3 font-semibold text-muted-foreground" scope="col">Views</th>
                  <th className="text-right px-4 py-3 font-semibold text-muted-foreground hidden md:table-cell" scope="col">Impr.</th>
                </tr>
              </thead>
              <tbody>
                {TOP_PAGES.map((page) => (
                  <tr key={page.path} className="border-b border-border last:border-0 hover:bg-muted/30 transition-colors">
                    <td className="px-4 py-3">
                      <p className="font-medium text-foreground text-sm leading-snug">{page.title}</p>
                      <p className="text-[11px] font-mono text-muted-foreground mt-0.5 truncate">{page.path}</p>
                    </td>
                    <td className="px-4 py-3 text-right font-semibold text-foreground">{page.views.toLocaleString()}</td>
                    <td className="px-4 py-3 text-right text-muted-foreground hidden md:table-cell">{page.impressions.toLocaleString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      </div>

      {/* Site-wide SEO settings */}
      <div className="rounded-xl border border-border bg-card p-5 flex flex-col gap-4">
        <h2 className="text-base font-semibold text-foreground flex items-center gap-2">
          <Globe className="w-4 h-4" />
          Default Social / Open Graph Settings
        </h2>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Default OG Title</label>
            <input
              className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
              defaultValue={SOCIAL_PREVIEW.title}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Default OG Description</label>
            <input
              className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
              defaultValue={SOCIAL_PREVIEW.description}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Sitemap URL</label>
            <div className="flex items-center gap-2">
              <input
                className="flex-1 px-3 py-2 rounded-lg border border-border bg-background text-sm font-mono focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
                defaultValue="https://thetriangle.org/sitemap.xml"
                readOnly
              />
              <button className="p-2 rounded-lg border border-border text-muted-foreground hover:bg-muted transition-colors" type="button" title="Open sitemap">
                <Link2 className="w-4 h-4" />
              </button>
            </div>
          </div>
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Robots.txt</label>
            <div className="flex items-center gap-2">
              <input
                className="flex-1 px-3 py-2 rounded-lg border border-border bg-background text-sm font-mono focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
                defaultValue="https://thetriangle.org/robots.txt"
                readOnly
              />
              <button className="p-2 rounded-lg border border-border text-muted-foreground hover:bg-muted transition-colors" type="button" title="Open robots.txt">
                <Link2 className="w-4 h-4" />
              </button>
            </div>
          </div>
        </div>
        <div className="flex justify-end">
          <button className="px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors" type="button">
            Save Changes
          </button>
        </div>
      </div>
    </div>
  )
}
