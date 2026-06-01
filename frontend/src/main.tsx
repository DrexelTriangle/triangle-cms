import React from "react"
import ReactDOM from "react-dom/client"
import { BrowserRouter } from "react-router-dom"
import App from "./App.tsx"
import { SessionAuthProvider } from "./auth/sessionAuth.tsx"
import "./index.css"

if (window.location.hostname === "127.0.0.1") {
  const next = `${window.location.protocol}//localhost:${window.location.port}${window.location.pathname}${window.location.search}${window.location.hash}`
  window.location.replace(next)
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <SessionAuthProvider>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </SessionAuthProvider>
  </React.StrictMode>
)
