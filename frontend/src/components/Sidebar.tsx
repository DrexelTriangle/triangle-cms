import { Sidebar as KumoSidebar } from "@cloudflare/kumo"
import { FileTextIcon, HouseIcon, ImageIcon } from "@phosphor-icons/react"
import { useLocation, useNavigate } from "react-router-dom"
import logo from "../assets/logo.png"

const navItems = [
  {
    icon: HouseIcon,
    label: "Dashboard",
    path: "/",
  },
  {
    icon: FileTextIcon,
    label: "Article",
    path: "/articleView",
  },
  {
    icon: ImageIcon,
    label: "Media",
    path: "/mediaView",
  },
]

function Sidebar() {
  const { pathname } = useLocation()
  const navigate = useNavigate()

  return (
    <KumoSidebar className="triangle-kumo-sidebar">
      <KumoSidebar.Header>
        <button className="triangle-brand-link" onClick={() => navigate("/")} type="button">
          <img alt="Delta Logo" className="triangle-brand-logo" src={logo} />
          <span className="triangle-brand-text">Delta</span>
        </button>
      </KumoSidebar.Header>

      <KumoSidebar.Content>
        <KumoSidebar.Group>
          <KumoSidebar.Menu>
            {navItems.map((item) => {
              const isActive = pathname === item.path

              return (
                <KumoSidebar.MenuButton
                  key={item.path}
                  active={isActive}
                  icon={item.icon}
                  onClick={() => navigate(item.path)}
                  type="button"
                >
                  {item.label}
                </KumoSidebar.MenuButton>
              )
            })}
          </KumoSidebar.Menu>
        </KumoSidebar.Group>
      </KumoSidebar.Content>

      <KumoSidebar.Footer>
        <KumoSidebar.Trigger />
      </KumoSidebar.Footer>
    </KumoSidebar>
  )
}

export default Sidebar
