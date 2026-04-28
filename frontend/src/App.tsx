import './App.css'
import { Routes, Route } from "react-router-dom"
import { Sidebar as KumoSidebar } from "@cloudflare/kumo"
import Header from "./components/Header"
import Sidebar from "./components/Sidebar"
import ArticleView from "./pages/articleView"
import DevelopingStoriesView from "./pages/developingStoriesView"
import EditArticleView from "./pages/editArticleView"
import MediaView from "./pages/mediaView"



function App() {
  return (
    <div className="app">
      <KumoSidebar.Provider className="triangle-shell" defaultOpen>
        <div className="layout">
          <Sidebar />
          <div className="main-pane">
            <Header />
            <main className="content">
              <Routes>
                <Route path="/" element={<p>THIS IS THE HOMEPAGE</p>} />
                <Route path="/articles" element={<ArticleView excludeType="developing-stories" />} />
                <Route path="/articles/:slug/edit" element={<EditArticleView />} />
                <Route path="/developing-stories" element={<DevelopingStoriesView />} />
                <Route path="/developing-stories/:slug/edit" element={<EditArticleView />} />
                <Route path="/newsletter" element={<p>NEWSLETTER PAGE</p>} />
                <Route path="/media" element={<MediaView />} />
                <Route path="/authors" element={<p>AUTHORS PAGE</p>} />
                <Route path="/sections" element={<p>SECTIONS PAGE</p>} />
                <Route path="/categories" element={<p>CATEGORIES PAGE</p>} />
                <Route path="/seo" element={<p>SEO PAGE</p>} />
                <Route path="/users" element={<p>USERS PAGE</p>} />
                <Route path="/user-settings" element={<p>USER SETTINGS PAGE</p>} />
                <Route path="/settings" element={<p>SETTINGS PAGE</p>} />
                <Route path="/articleView" element={<ArticleView excludeType="developing-stories" />} />
                <Route path="/mediaView" element={<MediaView />} />
              </Routes>
            </main>
          </div>
        </div>
      </KumoSidebar.Provider>
    </div>
  )
}

export default App
