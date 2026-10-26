import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { beforeEach, describe, expect, it, vi } from "vitest"

import QuizEditor from "./quizEditor"

type Call = { url: string; method: string; body: Record<string, unknown> | null }
let calls: Call[] = []
let responses: Array<() => Response> = []
let referencesStatus = 200

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
  if (method === "GET" && url === "/v1/quizzes/7/references") return jsonResponse({ published: 2, scheduled: 0, draft: 1 }, referencesStatus)
  const next = responses.shift()
  return next ? next() : jsonResponse({}, 500)
})

vi.mock("../hooks/useApiFetch", () => ({
  useApiFetch: () => apiFetchStub,
}))
vi.mock("../components/MediaPicker", () => ({ default: () => null }))

const renderNewEditor = () => {
  const user = userEvent.setup()
  render(
    <MemoryRouter initialEntries={["/quizzes/new"]}>
      <Routes>
        <Route path="/quizzes/new" element={<QuizEditor />} />
      </Routes>
    </MemoryRouter>,
  )
  return user
}

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
    referencesStatus = 200
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
    expect(screen.getByText(/Starts are approximate: 7\./)).toBeInTheDocument()
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

  it("shows warnings from a successful save and offers no publish anyway", async () => {
    responses = [() => jsonResponse({ ...stored, edit_version: 4, warnings: ['No answer points at result "b"'] })]
    const user = await renderEditor()
    await user.click(screen.getByRole("button", { name: /^publish$/i }))
    expect(await screen.findByText(/No answer points at result "b"/)).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /publish anyway/i })).toBeNull()
  })

  it("sends nothing when the reset confirm is cancelled", async () => {
    responses = [() => jsonResponse({ reason: "reset_required" }, 409)]
    const user = await renderEditor()
    await user.click(screen.getByRole("button", { name: /^publish$/i }))
    await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: /^cancel$/i }))
    expect(screen.queryByRole("dialog")).toBeNull()
    expect(calls.filter((c) => c.method === "PATCH")).toHaveLength(1)
  })

  it("does not open the confirm when the references check fails", async () => {
    referencesStatus = 500
    const user = await renderEditor()
    await user.click(screen.getByRole("button", { name: /^unpublish$/i }))
    expect(await screen.findByText(/Could not check which articles contain this quiz/)).toBeInTheDocument()
    expect(screen.queryByRole("dialog")).toBeNull()
  })

  it("deletes through the confirm with the loaded edit_version", async () => {
    responses = [() => new Response(null, { status: 204 })]
    const user = await renderEditor()
    await user.click(screen.getByRole("button", { name: /^delete$/i }))
    await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: /^delete$/i }))
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true))
    const del = calls.find((c) => c.method === "DELETE")
    expect(del?.url).toBe("/v1/quizzes/7")
    expect(del?.body).toEqual({ edit_version: 3 })
  })

  it("creates a new quiz as a draft with POST", async () => {
    responses = [() => jsonResponse({ ...stored, status: "draft", edit_version: 1 }, 201)]
    const user = renderNewEditor()
    await user.type(screen.getAllByLabelText(/^title/i)[0], "My quiz")
    await user.click(screen.getByRole("button", { name: /save draft/i }))
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(1))
    const post = calls.find((c) => c.method === "POST")
    expect(post?.url).toBe("/v1/quizzes")
    expect(post?.body).toEqual({
      slug: "my-quiz",
      title: "My quiz",
      kind: "trivia",
      status: "draft",
      intro: "",
      cover_media_id: null,
      definition: {
        questions: [{ text: "", image_media_id: null, answers: [{ text: "", correct: false, result_key: null }, { text: "", correct: false, result_key: null }], explanation: "" }],
        results: [{ key: "0-1", min: 0, max: 1, title: "", body: "", image_media_id: null }],
      },
      edit_version: 1,
    })
  })

  it("clears correct marks and explanations when trivia becomes personality", async () => {
    responses = [() => jsonResponse({ ...stored, status: "draft", edit_version: 1 }, 201)]
    const user = renderNewEditor()
    await user.type(screen.getAllByLabelText(/^title/i)[0], "Switch")
    await user.click(screen.getByLabelText("Question 1 answer 1 correct"))
    await user.type(screen.getByLabelText(/^explanation/i), "Because")
    await user.click(screen.getByLabelText(/personality/i))
    await user.click(screen.getByRole("button", { name: /save draft/i }))
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(1))
    const sent = JSON.stringify(calls.find((c) => c.method === "POST")?.body)
    expect(sent).not.toContain('"correct":true')
    expect(sent).not.toContain("Because")
    expect(calls.find((c) => c.method === "POST")?.body).toMatchObject({ definition: { results: [] } })
  })

  it("resets results to a fresh band when personality becomes trivia", async () => {
    responses = [() => jsonResponse({ ...stored, status: "draft", edit_version: 1 }, 201)]
    const user = renderNewEditor()
    await user.type(screen.getAllByLabelText(/^title/i)[0], "Switch")
    await user.click(screen.getByLabelText(/personality/i))
    await user.click(screen.getByRole("button", { name: /add result/i }))
    await user.click(screen.getByLabelText(/trivia/i))
    await user.click(screen.getByRole("button", { name: /save draft/i }))
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(1))
    expect(calls.find((c) => c.method === "POST")?.body).toMatchObject({
      definition: { results: [{ key: "0-1", min: 0, max: 1, title: "", body: "", image_media_id: null }] },
    })
  })

  it("re-enables the buttons and shows a message when the save fetch throws", async () => {
    responses = [() => { throw new Error("offline") }]
    const user = renderNewEditor()
    await user.type(screen.getAllByLabelText(/^title/i)[0], "Offline")
    await user.click(screen.getByRole("button", { name: /save draft/i }))
    expect(await screen.findByText(/Save failed\. Check your connection/)).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /save draft/i })).toBeEnabled()
    expect(screen.getByRole("button", { name: /^publish$/i })).toBeEnabled()
  })
})
