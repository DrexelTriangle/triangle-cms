import { useEffect, useState } from "react"
import { Link, useNavigate } from "react-router-dom"
import { Plus } from "lucide-react"
import { useApiFetch } from "../hooks/useApiFetch"

export type QuizListItem = {
  id: number
  slug: string
  title: string
  kind: "trivia" | "personality"
  status: "draft" | "published"
  updated_at: string
  starts: number
  edit_version: number
}

const KIND_LABEL: Record<QuizListItem["kind"], string> = { trivia: "Trivia", personality: "Personality" }
const STATUS_LABEL: Record<QuizListItem["status"], string> = { draft: "Draft", published: "Published" }

export default function QuizzesPage() {
  const apiFetch = useApiFetch()
  const navigate = useNavigate()
  const [quizzes, setQuizzes] = useState<QuizListItem[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const res = await apiFetch("/v1/quizzes")
        if (!res.ok) throw new Error(`Failed to load quizzes (${res.status})`)
        const list = (await res.json()) as QuizListItem[]
        if (!cancelled) setQuizzes(list)
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : "Failed to load quizzes")
      }
    })()
    return () => {
      cancelled = true
    }
  }, [apiFetch])

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold text-foreground">Quizzes</h1>
        <Link className="inline-flex items-center gap-2 rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90" to="/quizzes/new">
          <Plus className="h-4 w-4" /> New quiz
        </Link>
      </div>
      {error && <p className="text-sm text-destructive">{error}</p>}
      {quizzes && quizzes.length === 0 && <p className="text-sm text-muted-foreground">No quizzes yet. Build one and insert it into a story from the editor's Quiz button.</p>}
      {quizzes && quizzes.length > 0 && (
        <table className="w-full text-sm">
          <thead className="text-left text-muted-foreground">
            <tr>
              <th className="py-2">Title</th>
              <th className="py-2">Kind</th>
              <th className="py-2">Status</th>
              <th className="py-2">Starts (approximate)</th>
              <th className="py-2">Updated</th>
            </tr>
          </thead>
          <tbody>
            {quizzes.map((q) => (
              // The whole row opens the editor (spec 6.1); the title stays a
              // real link so keyboard users can reach it.
              <tr className="cursor-pointer border-t border-border hover:bg-muted" key={q.id} onClick={() => navigate(`/quizzes/${q.id}`)}>
                <td className="py-2">
                  <Link className="text-primary underline" onClick={(e) => e.stopPropagation()} to={`/quizzes/${q.id}`}>
                    {q.title}
                  </Link>
                </td>
                <td className="py-2">{KIND_LABEL[q.kind]}</td>
                <td className="py-2">{STATUS_LABEL[q.status]}</td>
                <td className="py-2">{q.starts}</td>
                <td className="py-2">{new Date(q.updated_at).toLocaleDateString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}
