import { Sidebar as KumoSidebar } from "@cloudflare/kumo"
import { EnvelopeIcon, FileTextIcon, GearIcon, HouseIcon, ImageIcon, MagnifyingGlassIcon, PlantIcon, RowsIcon, SquaresFourIcon, TagIcon, UsersFourIcon, UsersIcon } from "@phosphor-icons/react"
import { useLocation, useNavigate } from "react-router-dom"
import logo from "../assets/logo.png"

const dashboardItems = [
  {
    icon: HouseIcon,
    label: "Home",
    path: "/",
  },
]

const contentItems = [
  {
    icon: FileTextIcon,
    label: "Articles",
    path: "/articles",
  },
  {
    icon: PlantIcon,
    label: "Developing Stories",
    path: "/developing-stories",
  },
  {
    icon: EnvelopeIcon,
    label: "Newsletter",
    path: "/newsletter",
  },
  {
    icon: ImageIcon,
    label: "Media",
    path: "/media",
  },
]

const manageItems = [
  {
    icon: UsersFourIcon,
    label: "Authors",
    path: "/authors",
  },
  {
    icon: RowsIcon,
    label: "Sections",
    path: "/sections",
  },
  {
    icon: SquaresFourIcon,
    label: "Categories",
    path: "/categories",
  },
  {
    icon: TagIcon,
    label: "Tags",
    path: "/tags",
  },
  {
    icon: MagnifyingGlassIcon,
    label: "SEO",
    path: "/seo",
  },
]

const adminItems = [
  {
    icon: UsersIcon,
    label: "Users",
    path: "/users",
  },
  {
    icon: GearIcon,
    label: "Settings",
    path: "/settings",
  },
]

function Sidebar() {
  const { pathname } = useLocation()
  const navigate = useNavigate()

  const renderMenuButtons = (items: Array<{ icon: typeof HouseIcon; label: string; path: string }>) =>
    items.map((item) => (
      <KumoSidebar.MenuButton
        key={item.path}
        active={pathname === item.path}
        icon={item.icon}
        onClick={() => navigate(item.path)}
        type="button"
      >
        {item.label}
      </KumoSidebar.MenuButton>
    ))

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
          <KumoSidebar.Menu>{renderMenuButtons(dashboardItems)}</KumoSidebar.Menu>
        </KumoSidebar.Group>

        <KumoSidebar.Separator />

        <KumoSidebar.Group collapsible defaultOpen>
          <KumoSidebar.GroupLabel>Content</KumoSidebar.GroupLabel>
          <KumoSidebar.GroupContent>
            <KumoSidebar.Menu>{renderMenuButtons(contentItems)}</KumoSidebar.Menu>
          </KumoSidebar.GroupContent>
        </KumoSidebar.Group>

        <KumoSidebar.Separator />

        <KumoSidebar.Group collapsible defaultOpen>
          <KumoSidebar.GroupLabel>Manage</KumoSidebar.GroupLabel>
          <KumoSidebar.GroupContent>
            <KumoSidebar.Menu>{renderMenuButtons(manageItems)}</KumoSidebar.Menu>
          </KumoSidebar.GroupContent>
        </KumoSidebar.Group>

        <KumoSidebar.Separator />

        <KumoSidebar.Group collapsible defaultOpen>
          <KumoSidebar.GroupLabel>Admin</KumoSidebar.GroupLabel>
          <KumoSidebar.GroupContent>
            <KumoSidebar.Menu>{renderMenuButtons(adminItems)}</KumoSidebar.Menu>
          </KumoSidebar.GroupContent>
        </KumoSidebar.Group>
      </KumoSidebar.Content>

      <KumoSidebar.Footer>
        <KumoSidebar.Trigger />
      </KumoSidebar.Footer>
    </KumoSidebar>
  )
}

export default Sidebar
