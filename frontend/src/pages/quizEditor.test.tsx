import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { beforeEach, describe, expect, it, vi } from "vitest"

import QuizEditor from "./quizEditor"

type Call = { url: string; method: string; body: Record<string, unknown> | null }
let calls: Call[] = []
let responses: Array<() => Response> = []

const jsonResponse = (payload: unknown, status = 200) =>
  new Response(JSON.stringify(payload), { status, headers: { "Content-Type": "application/json" } })

const stored = {
  id: 7, slug: "campus", title: "Campus trivia", kind: "trivia", status: "published", intro: "", cover_media_id: null, cover_url: null,
  definition: {
    questions: [{ text: "Q1", image_media_id: null, answers: [{ text: "a", correct: true, result_key: null }, { text: "b", correct: false, result_key: null }], explanation: "" }],
    results: [{ key: "0-1", min: 0, max: 1, title: "Band", body: "", image_media_id: null }],
  },
  definition_version: 1, edit_version: 3, counts: [[5, 2]], created_at: "", updated_at: "",
}

// One module-level stub, the same shape as editArticleView.test.tsx, so the
// editor's load effect sees a stable function and runs once.
const apiFetchStub = vi.fn(async (url: string, init?: RequestInit): Promise<Response> => {
  const method = (init?.method ?? "GET").toUpperCase()
  calls.push({ url, method, body: typeof init?.body === "string" ? JSON.parse(init.body) : null })
  if (method === "GET" && url === "/v1/quizzes/7") return jsonResponse(stored)
  if (method === "GET" && url === "/v1/quizzes/7/references") return jsonResponse({ published: 2, scheduled: 0, draft: 1 })
  const next = responses.shift()
  return next ? next() : jsonResponse({}, 500)
})

vi.mock("../hooks/useApiFetch", () => ({
  useApiFetch: () => apiFetchStub,
}))
vi.mock("../components/MediaPicker", () => ({ default: () => null }))

const renderEditor = async () => {
  const user = userEvent.setup()
  render(
    <MemoryRouter initialEntries={["/quizzes/7"]}>
      <Routes>
        <Route path="/quizzes/:id" element={<QuizEditor />} />
      </Routes>
    </MemoryRouter>,
  )
  await waitFor(() => expect(screen.getByDisplayValue("Campus trivia")).toBeInTheDocument())
  return user
}

describe("QuizEditor", () => {
  beforeEach(() => {
    calls = []
    responses = []
  })

  it("shows server validation errors above the form", async () => {
    responses = [() => jsonResponse({ errors: ["Question 1: exactly one correct answer is required"], warnings: [] }, 422)]
    const user = await renderEditor()
    await user.click(screen.getByRole("button", { name: /^publish$/i }))
    expect(await screen.findByText(/Question 1: exactly one correct answer/)).toBeInTheDocument()
  })

  it("confirms a reset and resends with the flag and the same edit_version", async () => {
    responses = [() => jsonResponse({ reason: "reset_required" }, 409), () => jsonResponse(stored)]
    const user = await renderEditor()
    await user.click(screen.getByRole("button", { name: /^publish$/i }))
    expect(await screen.findByText(/answer counts for this quiz will be reset/i)).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: /save and reset/i }))
    await waitFor(() => expect(calls.filter((c) => c.method === "PATCH")).toHaveLength(2))
    const [first, second] = calls.filter((c) => c.method === "PATCH")
    expect(first.url).toBe("/v1/quizzes/7")
    expect(second.url).toBe("/v1/quizzes/7?reset_counts=true")
    expect(second.body?.edit_version).toBe(3)
  })

  it("tells the editor to reload on a stale save", async () => {
    responses = [() => jsonResponse({ reason: "stale" }, 409)]
    const user = await renderEditor()
    await user.click(screen.getByRole("button", { name: /^publish$/i }))
    expect(await screen.findByText(/Someone else saved this quiz/)).toBeInTheDocument()
  })

  it("locks the slug once published and shows results", async () => {
    const user = await renderEditor()
    expect(screen.getByLabelText(/slug/i)).toHaveAttribute("readonly")
    await user.click(screen.getByRole("button", { name: /^results$/i }))
    expect(screen.getByText(/of submitted answers/i)).toBeInTheDocument()
    expect(screen.getByText("71%")).toBeInTheDocument()
  })

  it("gives a published quiz no save draft button", async () => {
    await renderEditor()
    expect(screen.queryByRole("button", { name: /save draft/i })).toBeNull()
  })

  it("names referencing articles before unpublishing, then saves as a draft", async () => {
    responses = [() => jsonResponse({ ...stored, status: "draft", edit_version: 4 })]
    const user = await renderEditor()
    await user.click(screen.getByRole("button", { name: /^unpublish$/i }))
    expect(await screen.findByText(/2 published and 1 draft article/)).toBeInTheDocument()
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: /^unpublish$/i }))
    await waitFor(() => expect(calls.filter((c) => c.method === "PATCH")).toHaveLength(1))
    expect(calls.find((c) => c.method === "PATCH")?.body?.status).toBe("draft")
  })
})
