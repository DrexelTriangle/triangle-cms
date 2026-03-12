import './App.css'
import { Routes, Route } from "react-router-dom"
import Header from "./components/Header"
import Sidebar from "./components/Sidebar"
import ArticleView from "./pages/articleView"
import MediaView from "./pages/mediaView"



function App() {
  return (
    <div className="app">
      <Header />
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
    </div>
  )
}

export default App