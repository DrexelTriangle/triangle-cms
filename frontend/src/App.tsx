import { lazy, Suspense } from "react"
import { useCurrentUserRole } from "./hooks/useCurrentUserRole"
import { Routes, Route, useLocation, Navigate } from "react-router-dom"
import { useSessionAuth } from "./auth/sessionAuthContext"
import Header from "./components/Header"
import Sidebar from "./components/Sidebar"

// Keep entry routes in the main bundle to avoid a second download on sign-in.
import DashboardPage from "./pages/DashboardPage"
import LoginPage from "./pages/LoginPage"
import AuthCallback from "./pages/AuthCallback"

// Lazy-load feature routes, including the Trix article editor.
const ArticleView = lazy(() => import("./pages/articleView"))
const DevelopingStoriesView = lazy(() => import("./pages/developingStoriesView"))
const EditArticleView = lazy(() => import("./pages/editArticleView"))
const MediaView = lazy(() => import("./pages/mediaView"))
const AuthorsView = lazy(() => import("./pages/authorsView"))
const SectionsView = lazy(() => import("./pages/sectionsView"))
const UsersView = lazy(() => import("./pages/usersView"))
const CommentsView = lazy(() => import("./pages/commentsView"))
const ClassifiedsView = lazy(() => import("./pages/classifiedsView"))
const ActivityView = lazy(() => import("./pages/activityView"))
const SeoView = lazy(() => import("./pages/seoView"))
const SettingsPage = lazy(() => import("./pages/settingsPage"))
const PollView = lazy(() => import("./pages/pollView"))

const AUTH_ROUTES = ["/login"]

function RouteFallback() {
  return (
    <div className="flex items-center justify-center h-full">
      <p className="text-sm text-muted-foreground">Loading...</p>
    </div>
  )
}

function AppShell({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-screen overflow-hidden bg-background">
      <Sidebar />
      <div className="flex flex-col flex-1 min-w-0 overflow-hidden">
        <Header />
        <main className="flex-1 overflow-y-auto">{children}</main>
      </div>
    </div>
  )
}

function AdminOnlyRoute({ children }: { children: React.ReactNode }) {
  const { isAdmin, isLoading } = useCurrentUserRole()

  if (isLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <p className="text-sm text-muted-foreground">Loading...</p>
      </div>
    )
  }

  if (!isAdmin) {
    return <Navigate to="/" replace />
  }

  return <>{children}</>
}

export default function App() {
  const location = useLocation()
  const auth = useSessionAuth()
  const isAuthRoute = AUTH_ROUTES.includes(location.pathname)

  if (auth.isLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <p className="text-sm text-muted-foreground">Loading...</p>
      </div>
    )
  }

  if (isAuthRoute || location.pathname === "/auth/callback") {
    return (
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/auth/callback" element={<AuthCallback />} />
      </Routes>
    )
  }

  if (!auth.isAuthenticated) {
    if (auth.hasPendingAuthFlow) {
      return (
        <div className="min-h-screen flex items-center justify-center">
          <p className="text-sm text-muted-foreground">Finalizing sign-in...</p>
        </div>
      )
    }
    return <Navigate to="/login" replace />
  }

  return (
    <AppShell>
      <Suspense fallback={<RouteFallback />}>
        <Routes>
          <Route path="/" element={<DashboardPage />} />
          <Route path="/articles" element={<ArticleView excludeType="developing-stories" />} />
          <Route path="/articles/:id/:slug/edit" element={<EditArticleView />} />
          <Route path="/articles/:slug/edit" element={<EditArticleView />} />
          <Route path="/developing-stories" element={<DevelopingStoriesView />} />
          <Route path="/developing-stories/:id/:slug/edit" element={<EditArticleView />} />
          <Route path="/developing-stories/:slug/edit" element={<EditArticleView />} />
          <Route path="/articles/new" element={<EditArticleView />} />
          <Route path="/developing-stories/new" element={<Navigate to="/developing-stories" replace />} />
          <Route path="/media" element={<MediaView />} />
          <Route path="/poll" element={<PollView />} />
          <Route path="/authors" element={<AuthorsView />} />
          <Route path="/sections" element={<SectionsView />} />
          <Route path="/comments" element={<CommentsView />} />
          <Route path="/classifieds" element={<ClassifiedsView />} />
          <Route path="/seo" element={<SeoView />} />
          <Route
            path="/activity"
            element={(
              <AdminOnlyRoute>
                <ActivityView />
              </AdminOnlyRoute>
            )}
          />
          <Route
            path="/users"
            element={(
              <AdminOnlyRoute>
                <UsersView />
              </AdminOnlyRoute>
            )}
          />
          <Route
            path="/settings"
            element={(
              <AdminOnlyRoute>
                <SettingsPage />
              </AdminOnlyRoute>
            )}
          />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </Suspense>
    </AppShell>
  )
}
