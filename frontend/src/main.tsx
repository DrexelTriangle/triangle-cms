import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './components/App.tsx'
import Header from './components/header.tsx'
import Sidebar from './components/Sidebar.tsx'

createRoot(document.getElementById('root')!).render(
  <StrictMode>

    <App />
    <Header />
    <Sidebar />

  </StrictMode>,
)
