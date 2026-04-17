import './App.css'
import { Routes, Route } from "react-router-dom"
import { Sidebar as KumoSidebar } from "@cloudflare/kumo"
import Sidebar from "./components/Sidebar"
import ArticleView from "./pages/articleView"
import MediaView from "./pages/mediaView"



function App() {
  return (
    <div className="app">
      <KumoSidebar.Provider defaultOpen>
        <div className="layout">
          <Sidebar />
          <div className="content">
            <Routes>
              <Route path="/" element={<p>THIS IS THE HOMEPAGE</p>} />
              <Route path="/articleView" element={<ArticleView />} />
              <Route path="/mediaView" element={<MediaView />} />
            </Routes>
          </div>
        </div>
      </KumoSidebar.Provider>
    </div>
  )
}

export default App
