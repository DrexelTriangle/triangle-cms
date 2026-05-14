import { Routes, Route, useLocation, Navigate } from "react-router-dom"
import { useAuth } from "react-oidc-context"
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
import CategoriesView from "./pages/categoriesView"
import TagsView from "./pages/tagsView"
import SectionsView from "./pages/sectionsView"
import UsersView from "./pages/usersView"
import CommentsView from "./pages/commentsView"
import ContactView from "./pages/contactView"
import ActivityView from "./pages/activityView"
import NewsletterView from "./pages/newsletterView"
import SeoView from "./pages/seoView"
import AdLocationsView from "./pages/adLocationsView"
import PagesView from "./pages/pagesView"
import SettingsPage from "./pages/settingsPage"

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

export default function App() {
  const location = useLocation()
  const auth = useAuth()
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
        <Route path="/articles/new" element={<ComingSoon page="New Article" />} />
        <Route path="/developing-stories/new" element={<ComingSoon page="New Developing Story" />} />
        <Route path="/newsletter" element={<NewsletterView />} />
        <Route path="/media" element={<MediaView />} />
        <Route path="/pages" element={<PagesView />} />
        <Route path="/ad-locations" element={<AdLocationsView />} />
        <Route path="/authors" element={<AuthorsView />} />
        <Route path="/sections" element={<SectionsView />} />
        <Route path="/categories" element={<CategoriesView />} />
        <Route path="/tags" element={<TagsView />} />
        <Route path="/comments" element={<CommentsView />} />
        <Route path="/contact" element={<ContactView />} />
        <Route path="/seo" element={<SeoView />} />
        <Route path="/activity" element={<ActivityView />} />
        <Route path="/users" element={<UsersView />} />
        <Route path="/user-settings" element={<ComingSoon page="Profile Settings" />} />
        <Route path="/settings" element={<SettingsPage />} />
        <Route path="/articleView" element={<ArticleView excludeType="developing-stories" />} />
        <Route path="/mediaView" element={<MediaView />} />
      </Routes>
    </AppShell>
  )
}
