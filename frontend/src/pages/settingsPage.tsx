import { ArrowDown, ArrowUp, LogOut, Plus, RefreshCw, Trash2 } from "lucide-react"
import { useCallback, useEffect, useRef, useState } from "react"
import { useApiFetch } from "../hooks/useApiFetch"
import { useCurrentUserRole } from "../hooks/useCurrentUserRole"
import { useNavigate } from "react-router-dom"
import { useSessionAuth } from "../auth/sessionAuthContext"

type IndexStatus = {
  running: boolean
  started_at?: string
  finished_at?: string
  progress?: { walked?: number; scanned?: number; added?: number; skipped?: number }
  error?: string
}

type CarouselSlide = {
  enabled: boolean
  title: string
  link_url: string
  image_url: string
  background_color: string
  text_color: string
  desktop_only: boolean
}

const emptyCarouselSlide = (): CarouselSlide => ({
  enabled: true,
  title: "",
  link_url: "",
  image_url: "",
  background_color: "",
  text_color: "#ffffff",
  desktop_only: false,
})

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">{label}</label>
      <div className="text-sm text-foreground">{children}</div>
    </div>
  )
}

export default function SettingsPage() {
  const { user, logout } = useSessionAuth()
  const { isAdmin } = useCurrentUserRole()
  const navigate = useNavigate()
  const apiFetch = useApiFetch()
  const name = user?.name ?? "—"
  const email = user?.email ?? "—"
  const username = user?.email ?? "—"
  const groups: string[] = []
  const initials = name.split(" ").map((n: string) => n[0]).join("").slice(0, 2).toUpperCase()
  const [siteTitle, setSiteTitle] = useState("")
  const [siteTitleDraft, setSiteTitleDraft] = useState("")
  const [siteTitleSaving, setSiteTitleSaving] = useState(false)
  const [siteTitleMessage, setSiteTitleMessage] = useState<string | null>(null)
  const [taxonomyRebuildRunning, setTaxonomyRebuildRunning] = useState(false)
  const [taxonomyRebuildMessage, setTaxonomyRebuildMessage] = useState<string | null>(null)
  const [breakingEnabled, setBreakingEnabled] = useState(false)
  const [breakingText, setBreakingText] = useState("")
  const [breakingSaved, setBreakingSaved] = useState<{ enabled: boolean; text: string }>({ enabled: false, text: "" })
  const [breakingSaving, setBreakingSaving] = useState(false)
  const [breakingMessage, setBreakingMessage] = useState<string | null>(null)
  const [carouselSlides, setCarouselSlides] = useState<CarouselSlide[]>([])
  const [carouselSaved, setCarouselSaved] = useState<CarouselSlide[]>([])
  const [carouselSaving, setCarouselSaving] = useState(false)
  const [carouselMessage, setCarouselMessage] = useState<string | null>(null)
  const [mediaIndexRunning, setMediaIndexRunning] = useState(false)
  const [mediaIndexMessage, setMediaIndexMessage] = useState<string | null>(null)
  // Guards against two poll loops (mount-resume plus a click) racing each other.
  const mediaPollActive = useRef(false)

  useEffect(() => {
    let cancelled = false
    async function loadSiteSettings() {
      try {
        const res = await apiFetch("/v1/settings/site")
        if (!res.ok) throw new Error(`Failed to load site settings (${res.status})`)
        const body = (await res.json()) as { site_title?: string }
        const title = String(body.site_title ?? "").trim()
        if (!cancelled) {
          setSiteTitle(title)
          setSiteTitleDraft(title)
        }
      } catch (err) {
        if (!cancelled) {
          setSiteTitleMessage(err instanceof Error ? err.message : "Failed to load site settings")
        }
      }
    }
    loadSiteSettings()
    return () => {
      cancelled = true
    }
  }, [apiFetch])

  useEffect(() => {
    let cancelled = false
    async function loadBreakingNews() {
      try {
        const res = await apiFetch("/v1/settings/breaking-news")
        if (!res.ok) throw new Error(`Failed to load breaking-news settings (${res.status})`)
        const body = (await res.json()) as { enabled?: boolean; text?: string }
        const enabled = Boolean(body.enabled)
        const text = String(body.text ?? "")
        if (!cancelled) {
          setBreakingEnabled(enabled)
          setBreakingText(text)
          setBreakingSaved({ enabled, text })
        }
      } catch (err) {
        if (!cancelled) {
          setBreakingMessage(err instanceof Error ? err.message : "Failed to load breaking-news settings")
        }
      }
    }
    loadBreakingNews()
    return () => {
      cancelled = true
    }
  }, [apiFetch])

  useEffect(() => {
    let cancelled = false
    async function loadHomepageCarousel() {
      try {
        const res = await apiFetch("/v1/settings/homepage-carousel")
        if (!res.ok) throw new Error(`Failed to load homepage carousel settings (${res.status})`)
        const body = (await res.json()) as { slides?: CarouselSlide[] }
        const slides = Array.isArray(body.slides) ? body.slides : []
        if (!cancelled) {
          setCarouselSlides(slides)
          setCarouselSaved(slides)
        }
      } catch (err) {
        if (!cancelled) {
          setCarouselMessage(err instanceof Error ? err.message : "Failed to load homepage carousel settings")
        }
      }
    }
    loadHomepageCarousel()
    return () => {
      cancelled = true
    }
  }, [apiFetch])

  async function saveBreakingNews() {
    const text = breakingText.trim()
    if (breakingEnabled && !text) {
      setBreakingMessage("Banner text is required when the banner is enabled")
      return
    }

    setBreakingSaving(true)
    setBreakingMessage(null)
    try {
      const res = await apiFetch("/v1/settings/breaking-news", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enabled: breakingEnabled, text }),
      })
      if (!res.ok) throw new Error(`Failed to save breaking-news settings (${res.status})`)
      const body = (await res.json()) as { enabled?: boolean; text?: string }
      const savedEnabled = Boolean(body.enabled)
      const savedText = String(body.text ?? "")
      setBreakingEnabled(savedEnabled)
      setBreakingText(savedText)
      setBreakingSaved({ enabled: savedEnabled, text: savedText })
      setBreakingMessage("Saved")
    } catch (err) {
      setBreakingMessage(err instanceof Error ? err.message : "Failed to save breaking-news settings")
    } finally {
      setBreakingSaving(false)
    }
  }

  async function saveSiteTitle() {
    const nextTitle = siteTitleDraft.trim()
    if (!nextTitle) {
      setSiteTitleMessage("Site title cannot be empty")
      return
    }

    setSiteTitleSaving(true)
    setSiteTitleMessage(null)
    try {
      const res = await apiFetch("/v1/settings/site", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ site_title: nextTitle }),
      })
      if (!res.ok) throw new Error(`Failed to save site settings (${res.status})`)
      setSiteTitle(nextTitle)
      setSiteTitleDraft(nextTitle)
      setSiteTitleMessage("Saved")
    } catch (err) {
      setSiteTitleMessage(err instanceof Error ? err.message : "Failed to save site settings")
    } finally {
      setSiteTitleSaving(false)
    }
  }

  function updateCarouselSlide(index: number, patch: Partial<CarouselSlide>) {
    setCarouselSlides((current) => current.map((slide, idx) => (idx === index ? { ...slide, ...patch } : slide)))
  }

  function moveCarouselSlide(index: number, direction: -1 | 1) {
    setCarouselSlides((current) => {
      const nextIndex = index + direction
      if (nextIndex < 0 || nextIndex >= current.length) return current
      const next = [...current]
      const [slide] = next.splice(index, 1)
      next.splice(nextIndex, 0, slide)
      return next
    })
  }

  function removeCarouselSlide(index: number) {
    setCarouselSlides((current) => current.filter((_, idx) => idx !== index))
  }

  async function saveHomepageCarousel() {
    const slides = carouselSlides.map((slide) => ({
      ...slide,
      title: slide.title.trim(),
      link_url: slide.link_url.trim(),
      image_url: slide.image_url.trim(),
      background_color: slide.background_color.trim(),
      text_color: slide.text_color.trim() || "#ffffff",
    }))
    const invalidIndex = slides.findIndex((slide) => slide.enabled && (!slide.link_url || (!slide.title && !slide.image_url)))
    if (invalidIndex >= 0) {
      setCarouselMessage(`Slide ${String(invalidIndex + 1)} needs a link and either a title or image`)
      return
    }

    setCarouselSaving(true)
    setCarouselMessage(null)
    try {
      const res = await apiFetch("/v1/settings/homepage-carousel", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ slides }),
      })
      if (!res.ok) throw new Error(`Failed to save homepage carousel settings (${res.status})`)
      const body = (await res.json()) as { slides?: CarouselSlide[] }
      const savedSlides = Array.isArray(body.slides) ? body.slides : []
      setCarouselSlides(savedSlides)
      setCarouselSaved(savedSlides)
      setCarouselMessage("Saved")
    } catch (err) {
      setCarouselMessage(err instanceof Error ? err.message : "Failed to save homepage carousel settings")
    } finally {
      setCarouselSaving(false)
    }
  }

  async function rebuildTaxonomyCounts() {
    setTaxonomyRebuildRunning(true)
    setTaxonomyRebuildMessage(null)
    try {
      const res = await apiFetch("/v1/settings/taxonomy/rebuild", {
        method: "POST",
      })
      if (!res.ok) throw new Error(`Failed to rebuild taxonomy counts (${res.status})`)
      setTaxonomyRebuildMessage("Taxonomy article counts rebuilt.")
    } catch (err) {
      setTaxonomyRebuildMessage(err instanceof Error ? err.message : "Failed to rebuild taxonomy counts")
    } finally {
      setTaxonomyRebuildRunning(false)
    }
  }

  // The index walks ~145k filesystem entries and takes minutes, so the server runs
  // it in the background and we poll. A synchronous request could never finish:
  // Nginx cuts an upstream read at 60s and Cloudflare at ~100s.
  const followMediaIndex = useCallback(async () => {
    // A second caller would double the poll rate and fight over the message.
    if (mediaPollActive.current) return
    mediaPollActive.current = true
    setMediaIndexRunning(true)
    try {
      for (;;) {
        const res = await apiFetch("/v1/media/index")
        if (!res.ok) throw new Error(`Failed to read index status (${res.status})`)
        const status = (await res.json()) as IndexStatus
        const p = status.progress

        if (!status.running) {
          if (status.error) {
            setMediaIndexMessage(`Reindex failed: ${status.error}`)
          } else if (status.finished_at) {
            setMediaIndexMessage(
              `Indexed ${String(p?.scanned ?? 0)} assets — ${String(p?.added ?? 0)} new, ${String(p?.skipped ?? 0)} already known.`,
            )
          }
          return
        }

        setMediaIndexMessage(`Indexing… ${String(p?.walked ?? 0)} files scanned, ${String(p?.added ?? 0)} added`)
        await new Promise((resolve) => setTimeout(resolve, 2000))
      }
    } catch (err) {
      setMediaIndexMessage(err instanceof Error ? err.message : "Failed to read index status")
    } finally {
      mediaPollActive.current = false
      setMediaIndexRunning(false)
    }
  }, [apiFetch])

  // An index started before this page was opened (or by another admin) is still
  // running server-side, so pick up its progress rather than showing an idle button.
  useEffect(() => {
    if (!isAdmin) return
    let cancelled = false
    async function resumeMediaIndex() {
      try {
        const res = await apiFetch("/v1/media/index")
        if (!res.ok) return
        const status = (await res.json()) as IndexStatus
        if (!cancelled && status.running) void followMediaIndex()
      } catch {
        // Nothing to resume, and this is not worth an error banner on load.
      }
    }
    void resumeMediaIndex()
    return () => {
      cancelled = true
    }
  }, [apiFetch, followMediaIndex, isAdmin])

  async function reindexMedia() {
    setMediaIndexMessage(null)
    try {
      const res = await apiFetch("/v1/media/index", { method: "POST" })
      // 409 means one is already running — join its progress rather than error.
      if (!res.ok && res.status !== 409) {
        throw new Error(`Failed to start reindex (${res.status})`)
      }
      await followMediaIndex()
    } catch (err) {
      setMediaIndexMessage(err instanceof Error ? err.message : "Failed to start reindex")
    }
  }

  const carouselDirty = JSON.stringify(carouselSlides) !== JSON.stringify(carouselSaved)

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Settings</h1>
          <p className="text-sm text-muted-foreground mt-0.5">Your account information</p>
        </div>
        <button
          type="button"
          onClick={async () => {
            await logout()
            navigate("/login", { replace: true })
          }}
          className="inline-flex items-center gap-2 px-4 py-2 rounded-lg border border-destructive/40 text-destructive text-sm font-medium hover:bg-destructive/10 transition-colors"
        >
          <LogOut className="w-4 h-4" />
          Sign out
        </button>
      </div>

      <div className="rounded-xl border border-border bg-card p-6 flex flex-col gap-6">
        <div className="flex items-center gap-4">
          <div className="w-16 h-16 rounded-full bg-primary flex items-center justify-center text-white text-xl font-bold shrink-0">
            {initials}
          </div>
          <div>
            <p className="text-base font-semibold text-foreground">{name}</p>
            <p className="text-sm text-muted-foreground">{email}</p>
          </div>
        </div>

        <div className="border-t border-border pt-6 grid grid-cols-1 md:grid-cols-2 gap-6">
          <Field label="Full Name">{name}</Field>
          <Field label="Username">{username}</Field>
          <Field label="Email">{email}</Field>
          <Field label="Groups">
            {groups.length > 0 ? (
              <div className="flex flex-wrap gap-1.5">
                {groups.map((g) => (
                  <span key={g} className="px-2.5 py-0.5 rounded-full bg-primary/10 text-primary text-xs font-medium">
                    {g}
                  </span>
                ))}
              </div>
            ) : "—"}
          </Field>
        </div>
      </div>

      <div className="rounded-xl border border-border bg-card p-6 flex flex-col gap-4">
        <h2 className="text-lg font-semibold text-foreground">Site</h2>
        <p className="text-sm text-muted-foreground">Public site title used by the frontend.</p>
        <div className="flex flex-col gap-2 max-w-xl">
          <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Site Title</label>
          <input
            value={siteTitleDraft}
            onChange={(e) => setSiteTitleDraft(e.target.value)}
            className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
            placeholder="The Triangle"
          />
        </div>
        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={saveSiteTitle}
            disabled={siteTitleSaving || siteTitleDraft.trim() === "" || siteTitleDraft.trim() === siteTitle}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-60"
          >
            Save Site Title
          </button>
          {siteTitleMessage && <span className="text-sm text-muted-foreground">{siteTitleMessage}</span>}
        </div>
      </div>

      <div className="rounded-xl border border-border bg-card p-6 flex flex-col gap-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h2 className="text-lg font-semibold text-foreground">Homepage Carousel</h2>
            <p className="text-sm text-muted-foreground">
              Slides shown in Scalene's Splide carousel on the public homepage.
            </p>
          </div>
          <button
            type="button"
            onClick={() => setCarouselSlides((current) => [...current, emptyCarouselSlide()])}
            className="inline-flex items-center gap-2 px-3 py-2 rounded-lg border border-border text-sm text-foreground hover:bg-muted/40"
          >
            <Plus className="w-4 h-4" />
            Add Slide
          </button>
        </div>

        <div className="flex flex-col gap-3">
          {carouselSlides.length === 0 ? (
            <div className="rounded-lg border border-dashed border-border px-4 py-6 text-sm text-muted-foreground">
              No carousel slides configured.
            </div>
          ) : (
            carouselSlides.map((slide, index) => (
              <div key={index} className="rounded-lg border border-border bg-background p-4 flex flex-col gap-3">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <label className="flex items-center gap-3 cursor-pointer select-none">
                    <input
                      type="checkbox"
                      checked={slide.enabled}
                      onChange={(e) => updateCarouselSlide(index, { enabled: e.target.checked })}
                      className="h-4 w-4 rounded border-border text-primary focus:ring-2 focus:ring-primary/40"
                    />
                    <span className="text-sm font-semibold text-foreground">Slide {index + 1}</span>
                  </label>
                  <div className="flex items-center gap-1">
                    <button
                      type="button"
                      onClick={() => moveCarouselSlide(index, -1)}
                      disabled={index === 0}
                      title="Move up"
                      className="p-1.5 rounded-lg text-muted-foreground hover:text-foreground hover:bg-muted/60 disabled:opacity-40"
                    >
                      <ArrowUp className="w-4 h-4" />
                    </button>
                    <button
                      type="button"
                      onClick={() => moveCarouselSlide(index, 1)}
                      disabled={index === carouselSlides.length - 1}
                      title="Move down"
                      className="p-1.5 rounded-lg text-muted-foreground hover:text-foreground hover:bg-muted/60 disabled:opacity-40"
                    >
                      <ArrowDown className="w-4 h-4" />
                    </button>
                    <button
                      type="button"
                      onClick={() => removeCarouselSlide(index)}
                      title="Delete"
                      className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </div>
                </div>

                <div className="grid grid-cols-1 lg:grid-cols-2 gap-3">
                  <div className="flex flex-col gap-2">
                    <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Title</label>
                    <input
                      value={slide.title}
                      onChange={(e) => updateCarouselSlide(index, { title: e.target.value })}
                      maxLength={160}
                      className="w-full px-3 py-2 rounded-lg border border-border bg-card text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
                      placeholder="Slide title"
                    />
                  </div>
                  <div className="flex flex-col gap-2">
                    <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Link URL</label>
                    <input
                      value={slide.link_url}
                      onChange={(e) => updateCarouselSlide(index, { link_url: e.target.value })}
                      className="w-full px-3 py-2 rounded-lg border border-border bg-card text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
                      placeholder="/classifieds or https://..."
                    />
                  </div>
                  <div className="flex flex-col gap-2">
                    <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Image URL</label>
                    <input
                      value={slide.image_url}
                      onChange={(e) => updateCarouselSlide(index, { image_url: e.target.value })}
                      className="w-full px-3 py-2 rounded-lg border border-border bg-card text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
                      placeholder="/images/banner.webp"
                    />
                  </div>
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                    <div className="flex flex-col gap-2">
                      <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Background</label>
                      <input
                        value={slide.background_color}
                        onChange={(e) => updateCarouselSlide(index, { background_color: e.target.value })}
                        className="w-full px-3 py-2 rounded-lg border border-border bg-card text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
                        placeholder="#275997"
                      />
                    </div>
                    <div className="flex flex-col gap-2">
                      <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Text Color</label>
                      <input
                        value={slide.text_color}
                        onChange={(e) => updateCarouselSlide(index, { text_color: e.target.value })}
                        className="w-full px-3 py-2 rounded-lg border border-border bg-card text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
                        placeholder="#ffffff"
                      />
                    </div>
                  </div>
                </div>

                <label className="flex items-center gap-3 cursor-pointer select-none">
                  <input
                    type="checkbox"
                    checked={slide.desktop_only}
                    onChange={(e) => updateCarouselSlide(index, { desktop_only: e.target.checked })}
                    className="h-4 w-4 rounded border-border text-primary focus:ring-2 focus:ring-primary/40"
                  />
                  <span className="text-sm text-foreground">Hide on mobile</span>
                </label>
              </div>
            ))
          )}
        </div>

        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={saveHomepageCarousel}
            disabled={carouselSaving || !carouselDirty}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-60"
          >
            Save Carousel
          </button>
          {carouselMessage && <span className="text-sm text-muted-foreground">{carouselMessage}</span>}
        </div>
      </div>

      <div className="rounded-xl border border-border bg-card p-6 flex flex-col gap-4">
        <h2 className="text-lg font-semibold text-foreground">Breaking News Banner</h2>
        <p className="text-sm text-muted-foreground">
          Show a breaking-news banner across the top of the public homepage.
        </p>
        <label className="flex items-center gap-3 cursor-pointer select-none">
          <input
            type="checkbox"
            checked={breakingEnabled}
            onChange={(e) => setBreakingEnabled(e.target.checked)}
            className="h-4 w-4 rounded border-border text-primary focus:ring-2 focus:ring-primary/40"
          />
          <span className="text-sm text-foreground">Enable banner</span>
        </label>
        <div className="flex flex-col gap-2 max-w-xl">
          <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Banner Text</label>
          <input
            value={breakingText}
            onChange={(e) => setBreakingText(e.target.value)}
            className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
            placeholder="Breaking news headline"
          />
        </div>
        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={saveBreakingNews}
            disabled={
              breakingSaving ||
              (breakingEnabled && breakingText.trim() === "") ||
              (breakingEnabled === breakingSaved.enabled && breakingText.trim() === breakingSaved.text)
            }
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-60"
          >
            Save Banner
          </button>
          {breakingMessage && <span className="text-sm text-muted-foreground">{breakingMessage}</span>}
        </div>
      </div>

      <div className="rounded-xl border border-border bg-card p-6 flex flex-col gap-4">
        <h2 className="text-lg font-semibold text-foreground">Taxonomy</h2>
        <p className="text-sm text-muted-foreground">
          Recalculate taxonomy article counts from current article category assignments.
        </p>
        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={rebuildTaxonomyCounts}
            disabled={taxonomyRebuildRunning}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-60"
          >
            Rebuild Taxonomy Counts
          </button>
          {taxonomyRebuildMessage && <span className="text-sm text-muted-foreground">{taxonomyRebuildMessage}</span>}
        </div>
      </div>

      {isAdmin && (
        <div className="rounded-xl border border-border bg-card p-6 flex flex-col gap-4">
          <h2 className="text-lg font-semibold text-foreground">Media Library</h2>
          <p className="text-sm text-muted-foreground">
            Scan the media filesystem for files missing from the library. This walks every asset on the
            media server and takes several minutes; existing entries are left untouched.
          </p>
          <div className="flex items-center gap-3">
            <button
              type="button"
              onClick={() => void reindexMedia()}
              disabled={mediaIndexRunning}
              className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-60"
            >
              <RefreshCw className={`w-4 h-4 ${mediaIndexRunning ? "animate-spin" : ""}`} aria-hidden="true" />
              {mediaIndexRunning ? "Reindexing…" : "Reindex Media"}
            </button>
            {mediaIndexMessage && <span className="text-sm text-muted-foreground">{mediaIndexMessage}</span>}
          </div>
        </div>
      )}
    </div>
  )
}
