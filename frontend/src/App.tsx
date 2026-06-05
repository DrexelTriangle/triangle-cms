import { useCurrentUserRole } from "./hooks/useCurrentUserRole"
import { Routes, Route, useLocation, Navigate } from "react-router-dom"
import { useSessionAuth } from "./auth/sessionAuthContext"
import Header from "./components/Header"
import Sidebar from "./components/Sidebar"
import DashboardPage from "./pages/DashboardPage"
import LoginPage from "./pages/LoginPage"
import SignupPage from "./pages/SignupPage"
import AuthCallback from "./pages/AuthCallback"
import ArticleView from "./pages/articleView"
import DevelopingStoriesView from "./pages/developingStoriesView"
import EditArticleView from "./pages/editArticleView"
import MediaView from "./pages/mediaView"
import AuthorsView from "./pages/authorsView"
import SectionsView from "./pages/sectionsView"
import UsersView from "./pages/usersView"
import CommentsView from "./pages/commentsView"
import ActivityView from "./pages/activityView"
import NewsletterView from "./pages/newsletterView"
import SeoView from "./pages/seoView"
import PagesView from "./pages/pagesView"
import SettingsPage from "./pages/settingsPage"
import PollView from "./pages/pollView"

const AUTH_ROUTES = ["/login", "/signup"]

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

function ComingSoon({ page }: { page: string }) {
  return (
    <div className="flex flex-col items-center justify-center h-full gap-3 text-muted-foreground">
      <div className="w-12 h-12 rounded-xl bg-muted flex items-center justify-center text-2xl">🚧</div>
      <p className="font-semibold text-foreground">{page}</p>
      <p className="text-sm">This page is coming soon.</p>
    </div>
  )
}

function AdminOnlyRoute({ children }: { children: React.ReactNode }) {
  const { isAdmin, isLoading } = useCurrentUserRole()

  if (isLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <p className="text-sm text-muted-foreground">Loading…</p>
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
        <p className="text-sm text-muted-foreground">Loading…</p>
      </div>
    )
  }

  if (isAuthRoute || location.pathname === "/auth/callback") {
    return (
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/signup" element={<SignupPage />} />
        <Route path="/auth/callback" element={<AuthCallback />} />
      </Routes>
    )
  }

  if (!auth.isAuthenticated) {
    if (auth.hasPendingAuthFlow) {
      return (
        <div className="min-h-screen flex items-center justify-center">
          <p className="text-sm text-muted-foreground">Finalizing sign-in…</p>
        </div>
      )
    }
    return <Navigate to="/login" replace />
  }

  return (
    <AppShell>
      <Routes>
        <Route path="/" element={<DashboardPage />} />
        <Route path="/articles" element={<ArticleView excludeType="developing-stories" />} />
        <Route path="/articles/:slug/edit" element={<EditArticleView />} />
        <Route path="/developing-stories" element={<DevelopingStoriesView />} />
        <Route path="/developing-stories/:slug/edit" element={<EditArticleView />} />
        <Route path="/articles/new" element={<EditArticleView />} />
        <Route path="/developing-stories/new" element={<ComingSoon page="New Developing Story" />} />
        <Route path="/newsletter" element={<NewsletterView />} />
        <Route path="/media" element={<MediaView />} />
        <Route path="/pages" element={<PagesView />} />
        <Route path="/poll" element={<PollView />} />
        <Route path="/authors" element={<AuthorsView />} />
        <Route path="/sections" element={<SectionsView />} />
        <Route path="/comments" element={<CommentsView />} />
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
        <Route path="/user-settings" element={<ComingSoon page="Profile Settings" />} />
        <Route
          path="/settings"
          element={(
            <AdminOnlyRoute>
              <SettingsPage />
            </AdminOnlyRoute>
          )}
        />
      </Routes>
    </AppShell>
  )
}
