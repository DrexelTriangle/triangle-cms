import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom"
import { describe, expect, it, vi } from "vitest"

import QuizzesPage from "./quizzesPage"

const jsonResponse = (payload: unknown, status = 200) =>
  new Response(JSON.stringify(payload), { status, headers: { "Content-Type": "application/json" } })

const apiFetchStub = vi.fn(async () =>
  jsonResponse([
    { id: 7, slug: "campus", title: "Campus trivia", kind: "trivia", status: "published", updated_at: "2026-10-12T15:00:00Z", starts: 42, edit_version: 3 },
    { id: 8, slug: "which", title: "Which building are you", kind: "personality", status: "draft", updated_at: "2026-10-11T15:00:00Z", starts: 0, edit_version: 1 },
  ]),
)

// Counts navigate() calls made by the page itself. Link navigates through
// react-router's internal copy, so a title click that also reaches the row
// handler shows up here.
const navigateSpy = vi.fn()
vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>()
  return {
    ...actual,
    useNavigate: () => {
      const navigate = actual.useNavigate()
      return (...args: Parameters<typeof navigate>) => {
        navigateSpy(args[0])
        return navigate(...args)
      }
    },
  }
})

vi.mock("../hooks/useApiFetch", () => ({
  useApiFetch: () => apiFetchStub,
}))

function CurrentPath() {
  return <p>At {useLocation().pathname}</p>
}

const renderPage = () =>
  render(
    <MemoryRouter initialEntries={["/quizzes"]}>
      <Routes>
        <Route path="/quizzes" element={<QuizzesPage />} />
        <Route path="/quizzes/:id" element={<CurrentPath />} />
      </Routes>
    </MemoryRouter>,
  )

describe("QuizzesPage", () => {
  it("lists quizzes with kind, status and approximate starts", async () => {
    renderPage()
    expect(await screen.findByRole("link", { name: /campus trivia/i })).toHaveAttribute("href", "/quizzes/7")
    expect(screen.getByText(/starts \(approximate\)/i)).toBeInTheDocument()
    expect(screen.getByText("42")).toBeInTheDocument()
    expect(screen.getByText("Personality")).toBeInTheDocument()
    expect(screen.getByText("Draft")).toBeInTheDocument()
    expect(screen.getByRole("link", { name: /new quiz/i })).toHaveAttribute("href", "/quizzes/new")
  })

  it("opens the editor from anywhere on the row", async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByText("Personality"))
    expect(await screen.findByText("At /quizzes/8")).toBeInTheDocument()
  })

  it("leaves navigation to the title link when it is clicked", async () => {
    navigateSpy.mockClear()
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole("link", { name: /which building/i }))
    expect(await screen.findByText("At /quizzes/8")).toBeInTheDocument()
    expect(navigateSpy.mock.calls.map((c) => c[0])).toEqual([])
  })
})
