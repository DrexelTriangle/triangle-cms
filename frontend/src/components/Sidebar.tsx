import { useState } from "react"
import { useLocation, useNavigate } from "react-router-dom"
import {
  LayoutDashboard,
  FileText,
  TrendingUp,
  Mail,
  Image,
  Users,
  Layers,
  Search,
  Settings,
  UserCog,
  ChevronLeft,
  ChevronRight,
  ChevronDown,
  LogOut,
  PlusCircle,
  BookOpen,
  MessageSquare,
  Activity,
  BarChart3,
} from "lucide-react"
import { cn } from "@/lib/utils"
import { Separator } from "@/components/ui/separator"
import logo from "../assets/logo.png"
import { useCurrentUserRole } from "../hooks/useCurrentUserRole"

type NavItem = {
  icon: React.ElementType
  label: string
  path: string
  badge?: number
  children?: { label: string; path: string }[]
}

type NavGroup = {
  label: string | null
  items: NavItem[]
}

const navGroups: NavGroup[] = [
  {
    label: null,
    items: [{ icon: LayoutDashboard, label: "Dashboard", path: "/" }],
  },
  {
    label: "Content",
    items: [
      { icon: FileText, label: "Articles", path: "/articles" },
      { icon: TrendingUp, label: "Developing Stories", path: "/developing-stories" },
      { icon: BookOpen, label: "Pages", path: "/pages" },
      { icon: BarChart3, label: "Poll", path: "/poll" },
      { icon: Image, label: "Media", path: "/media" },
      { icon: Mail, label: "Newsletter", path: "/newsletter" },
    ],
  },
  {
    label: "Manage",
    items: [
      { icon: Users, label: "Authors", path: "/authors" },
      { icon: Layers, label: "Sections", path: "/sections" },
      { icon: MessageSquare, label: "Comments", path: "/comments", badge: 3 },
      { icon: Search, label: "SEO", path: "/seo" },
    ],
  },
  {
    label: "Admin",
    items: [
      { icon: Activity, label: "Activity", path: "/activity" },
      { icon: UserCog, label: "Users", path: "/users" },
      { icon: Settings, label: "Settings", path: "/settings" },
    ],
  },
]

export default function Sidebar() {
  const { isAdmin } = useCurrentUserRole()
  const [collapsed, setCollapsed] = useState(false)
  const [openGroups, setOpenGroups] = useState<Record<string, boolean>>({
    "/articles": true,
  })
  const { pathname } = useLocation()
  const navigate = useNavigate()

  const toggleGroup = (path: string) => {
    setOpenGroups((prev) => ({ ...prev, [path]: !prev[path] }))
  }

  const isActive = (item: NavItem) => {
    if (item.children) {
      return item.children.some((c) => pathname === c.path) || pathname === item.path
    }
    return pathname === item.path
  }

  const visibleNavGroups = navGroups.filter((group) => group.label !== "Admin" || isAdmin)

  return (
    <aside
      className={cn(
        "relative flex flex-col h-screen bg-sidebar text-sidebar-foreground border-r border-sidebar-border transition-all duration-200 shrink-0",
        collapsed ? "w-[60px]" : "w-[220px]",
      )}
    >
      {/* Brand */}
      <div
        className={cn(
          "flex items-center gap-2.5 h-16 px-4 border-b border-sidebar-border shrink-0",
          collapsed && "justify-center px-0",
        )}
      >
        <img src={logo} alt="Delta" className="w-30 h-30 shrink-0 object-contain" />
        {/* {!collapsed && (
          <span className="text-lg font-bold text-white tracking-tight truncate">Delta CMS</span>
        )} */}
      </div>

      {/* Nav */}
      <nav className="flex-1 overflow-y-auto py-3 px-2">
        {visibleNavGroups.map((group, gi) => (
          <div key={gi} className="mb-1">
            {gi > 0 && <Separator className="my-2 bg-sidebar-border" />}
            {group.label && !collapsed && (
              <p className="text-[10px] font-semibold uppercase tracking-widest text-sidebar-muted px-3 pb-1.5 pt-0.5">
                {group.label}
              </p>
            )}
            {group.items.map((item) => {
              const Icon = item.icon
              const active = isActive(item)
              const hasChildren = item.children && item.children.length > 0
              const open = openGroups[item.path]

              return (
                <div key={item.path}>
                  <button
                    type="button"
                    title={collapsed ? item.label : undefined}
                    onClick={() => {
                      if (hasChildren && !collapsed) {
                        toggleGroup(item.path)
                      } else {
                        navigate(item.path)
                      }
                    }}
                    className={cn(
                      "w-full flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors",
                      collapsed && "justify-center px-2",
                      active
                        ? "bg-primary text-white"
                        : "text-sidebar-muted hover:bg-sidebar-accent hover:text-sidebar-foreground",
                    )}
                  >
                    <Icon className="w-4 h-4 shrink-0" />
                    {!collapsed && (
                      <>
                        <span className="flex-1 truncate text-left">{item.label}</span>
                        {item.badge ? (
                          <span className="ml-auto px-1.5 py-0.5 rounded-full text-[10px] font-bold bg-primary/20 text-primary min-w-[18px] text-center leading-none">
                            {item.badge}
                          </span>
                        ) : null}
                        {hasChildren && (
                          <ChevronDown
                            className={cn(
                              "w-3.5 h-3.5 shrink-0 transition-transform",
                              open && "rotate-180",
                            )}
                          />
                        )}
                      </>
                    )}
                  </button>

                  {/* Sub-items */}
                  {hasChildren && open && !collapsed && (
                    <div className="ml-3 pl-3 border-l border-sidebar-border mt-0.5 mb-1 space-y-0.5">
                      {item.children!.map((child) => (
                        <button
                          key={child.path}
                          type="button"
                          onClick={() => navigate(child.path)}
                          className={cn(
                            "w-full flex items-center gap-2 rounded-md px-3 py-1.5 text-xs font-medium transition-colors",
                            pathname === child.path
                              ? "text-white bg-primary/80"
                              : "text-sidebar-muted hover:bg-sidebar-accent hover:text-sidebar-foreground",
                          )}
                        >
                          {child.label === "Add New" && <PlusCircle className="w-3 h-3 shrink-0" />}
                          <span>{child.label}</span>
                        </button>
                      ))}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        ))}
      </nav>

      {/* Footer */}
      <div className={cn("px-2 py-3 border-t border-sidebar-border space-y-0.5 shrink-0")}>
        <button
          type="button"
          onClick={() => navigate("/login")}
          className={cn(
            "w-full flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium text-destructive hover:bg-destructive/10 transition-colors",
            collapsed && "justify-center px-2",
          )}
          title={collapsed ? "Log out" : undefined}
        >
          <LogOut className="w-4 h-4 shrink-0" />
          {!collapsed && <span>Log out</span>}
        </button>

        <button
          type="button"
          onClick={() => setCollapsed((c) => !c)}
          className={cn(
            "w-full flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium text-sidebar-muted hover:bg-sidebar-accent hover:text-sidebar-foreground transition-colors",
            collapsed && "justify-center px-2",
          )}
        >
          {collapsed ? <ChevronRight className="w-4 h-4" /> : <ChevronLeft className="w-4 h-4" />}
          {!collapsed && <span>Collapse</span>}
        </button>
      </div>
    </aside>
  )
}
