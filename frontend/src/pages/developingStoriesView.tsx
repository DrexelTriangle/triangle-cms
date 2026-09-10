import { useCallback, useEffect, useMemo, useState } from "react"
import type { FormEvent } from "react"
import { Plus, Trash2, RefreshCw, Pencil, Check, X } from "lucide-react"
import { useApiFetch } from "../hooks/useApiFetch"

type DevelopingStory = {
  title: string
  description?: string
}

type DevelopingStoriesResponse = {
  // Older servers sent bare titles; newer ones send {title, description}.
  stories?: Array<DevelopingStory | string>
}

const MAX_DESCRIPTION_LENGTH = 500

function normalizeStory(story: DevelopingStory | string): DevelopingStory {
  if (typeof story === "string") return { title: story, description: "" }
  return { title: story.title, description: story.description ?? "" }
}

async function readJson<T>(res: Response): Promise<T> {
  return (await res.json()) as T
}

async function readErrorMessage(res: Response, fallback: string) {
  try {
    const body = await readJson<{ error?: string }>(res)
    return body.error?.trim() || fallback
  } catch {
    return fallback
  }
}

function DevelopingStoriesView() {
  const apiFetch = useApiFetch()
  const [stories, setStories] = useState<DevelopingStory[]>([])
  const [newStoryTitle, setNewStoryTitle] = useState("")
  const [newStoryDescription, setNewStoryDescription] = useState("")
  const [editingTitle, setEditingTitle] = useState<string | null>(null)
  const [editingDescription, setEditingDescription] = useState("")
  const [searchQuery, setSearchQuery] = useState("")
  const [isLoading, setIsLoading] = useState(true)
  const [isSaving, setIsSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const loadStories = useCallback(async () => {
    setIsLoading(true)
    setError(null)
    try {
      const res = await apiFetch("/v1/developing-stories")
      if (!res.ok) throw new Error(await readErrorMessage(res, `Could not load developing stories (${res.status})`))
      const body = await readJson<DevelopingStoriesResponse>(res)
      setStories((body.stories ?? []).map(normalizeStory))
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not load developing stories.")
    } finally {
      setIsLoading(false)
    }
  }, [apiFetch])

  useEffect(() => {
    const handle = window.setTimeout(() => {
      void loadStories()
    }, 0)
    return () => window.clearTimeout(handle)
  }, [loadStories])

  const addStory = async (e: FormEvent) => {
    e.preventDefault()
    const title = newStoryTitle.trim()
    if (!title) return
    const description = newStoryDescription.trim()

    setIsSaving(true)
    setError(null)
    try {
      const res = await apiFetch("/v1/developing-stories", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ title, description }),
      })
      if (!res.ok) throw new Error(await readErrorMessage(res, `Could not add developing story (${res.status})`))
      setNewStoryTitle("")
      setNewStoryDescription("")
      await loadStories()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not add developing story.")
    } finally {
      setIsSaving(false)
    }
  }

  const startEditing = (story: DevelopingStory) => {
    setEditingTitle(story.title)
    setEditingDescription(story.description ?? "")
  }

  const cancelEditing = () => {
    setEditingTitle(null)
    setEditingDescription("")
  }

  const saveDescription = async (title: string) => {
    setIsSaving(true)
    setError(null)
    try {
      const res = await apiFetch("/v1/developing-stories", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ title, description: editingDescription.trim() }),
      })
      if (!res.ok) throw new Error(await readErrorMessage(res, `Could not update developing story (${res.status})`))
      cancelEditing()
      await loadStories()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not update developing story.")
    } finally {
      setIsSaving(false)
    }
  }

  const deleteStory = async (title: string) => {
    if (!confirm(`Delete developing story "${title}"?`)) return

    setIsSaving(true)
    setError(null)
    try {
      const res = await apiFetch("/v1/developing-stories", {
        method: "DELETE",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ title }),
      })
      if (!res.ok) throw new Error(await readErrorMessage(res, `Could not delete developing story (${res.status})`))
      await loadStories()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not delete developing story.")
    } finally {
      setIsSaving(false)
    }
  }

  const filteredStories = useMemo(() => {
    const query = searchQuery.trim().toLowerCase()
    if (!query) return stories
    return stories.filter(
      (story) =>
        story.title.toLowerCase().includes(query) ||
        (story.description ?? "").toLowerCase().includes(query),
    )
  }, [stories, searchQuery])

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Developing Stories</h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            {isLoading ? "Loading..." : `${stories.length} stor${stories.length === 1 ? "y" : "ies"}`}
          </p>
        </div>
        <button
          type="button"
          onClick={loadStories}
          disabled={isLoading || isSaving}
          className="inline-flex items-center gap-2 px-3 py-2 rounded-lg border border-border text-sm text-foreground hover:bg-muted/40 disabled:opacity-60"
        >
          <RefreshCw className="w-4 h-4" />
          Refresh
        </button>
      </div>

      <form onSubmit={addStory} className="flex flex-col gap-2 max-w-xl">
        <input
          value={newStoryTitle}
          onChange={(e) => setNewStoryTitle(e.target.value)}
          placeholder="Add developing story title"
          className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
          maxLength={200}
        />
        <textarea
          value={newStoryDescription}
          onChange={(e) => setNewStoryDescription(e.target.value)}
          placeholder="Description"
          className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
          maxLength={MAX_DESCRIPTION_LENGTH}
          rows={2}
        />
        <div className="flex items-center justify-end">
          <button
            type="submit"
            disabled={isSaving || !newStoryTitle.trim()}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-60"
          >
            <Plus className="w-4 h-4" />
            Add
          </button>
        </div>
      </form>

      <div className="relative">
        <input
          aria-label="Search developing stories"
          className="w-full max-w-xl px-3 py-2 rounded-lg border border-border bg-background text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
          onChange={(e) => setSearchQuery(e.target.value)}
          placeholder="Search developing stories"
          type="search"
          value={searchQuery}
        />
      </div>

      {error && (
        <div className="rounded-lg border border-destructive/30 bg-destructive/10 text-destructive text-sm px-3 py-2">
          {error}
        </div>
      )}

      <div className="rounded-xl border border-border bg-card overflow-hidden">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-border bg-muted/40">
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground">Title</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground">Description</th>
              <th className="text-right px-4 py-3 font-semibold text-muted-foreground">Actions</th>
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              <tr>
                <td className="px-4 py-8 text-center text-muted-foreground" colSpan={3}>Loading developing stories...</td>
              </tr>
            ) : filteredStories.length === 0 ? (
              <tr>
                <td className="px-4 py-8 text-center text-muted-foreground" colSpan={3}>
                  {searchQuery ? `No results for "${searchQuery}"` : "No developing stories found."}
                </td>
              </tr>
            ) : (
              filteredStories.map((story) => (
                <tr key={story.title} className="border-b border-border last:border-0 hover:bg-muted/30 transition-colors">
                  <td className="px-4 py-3 font-medium text-foreground align-top">{story.title}</td>
                  <td className="px-4 py-3 text-muted-foreground align-top">
                    {editingTitle === story.title ? (
                      <textarea
                        value={editingDescription}
                        onChange={(e) => setEditingDescription(e.target.value)}
                        className="w-full px-2 py-1 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
                        maxLength={MAX_DESCRIPTION_LENGTH}
                        rows={2}
                        autoFocus
                      />
                    ) : (
                      story.description
                    )}
                  </td>
                  <td className="px-4 py-3 align-top">
                    <div className="flex items-center justify-end gap-1">
                      {editingTitle === story.title ? (
                        <>
                          <button
                            type="button"
                            onClick={() => saveDescription(story.title)}
                            className="p-1.5 rounded-lg text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors"
                            title="Save description"
                            disabled={isSaving}
                          >
                            <Check className="w-4 h-4" />
                          </button>
                          <button
                            type="button"
                            onClick={cancelEditing}
                            className="p-1.5 rounded-lg text-muted-foreground hover:bg-muted/40 transition-colors"
                            title="Cancel"
                            disabled={isSaving}
                          >
                            <X className="w-4 h-4" />
                          </button>
                        </>
                      ) : (
                        <button
                          type="button"
                          onClick={() => startEditing(story)}
                          className="p-1.5 rounded-lg text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors"
                          title="Edit description"
                          disabled={isSaving}
                        >
                          <Pencil className="w-4 h-4" />
                        </button>
                      )}
                      <button
                        type="button"
                        onClick={() => deleteStory(story.title)}
                        className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors"
                        title="Delete"
                        disabled={isSaving}
                      >
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

export default DevelopingStoriesView
