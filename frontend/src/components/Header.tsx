import { Bell, Search } from "lucide-react"
import { useEffect, useState } from "react"
import { useAuth } from "react-oidc-context"
import { useNavigate } from "react-router-dom"
import { useApiFetch } from "../hooks/useApiFetch"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

export default function Header() {
  const navigate = useNavigate()
  const { user } = useAuth()
  const apiFetch = useApiFetch()
  const [displayRole, setDisplayRole] = useState("Editor")
  const profile = user?.profile
  const displayName = String(profile?.name ?? profile?.preferred_username ?? "Editor")
  const initials = displayName.split(" ").map((part) => part[0] ?? "").join("").slice(0, 2).toUpperCase()

  useEffect(() => {
    let cancelled = false

    async function loadMe() {
      try {
        const res = await apiFetch("/v1/users/me")
        if (!res.ok) return
        const body = (await res.json()) as { role?: string }
        const role = String(body.role ?? "").trim()
        if (!cancelled && role) {
          setDisplayRole(role.charAt(0).toUpperCase() + role.slice(1))
        }
      } catch {
        // Keep fallback role when /me is unavailable.
      }
    }

    void loadMe()
    return () => {
      cancelled = true
    }
  }, [apiFetch])

  return (
    <header className="h-16 flex items-center justify-between px-6 bg-card border-b border-border shrink-0">
      <div className="relative hidden sm:block">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
        <input
          placeholder="Search articles, authors..."
          className="h-9 w-64 pl-9 pr-3 text-sm rounded-lg border border-input bg-background placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-1"
        />
      </div>

      <div className="flex items-center gap-3 ml-auto">
        <Button variant="ghost" size="icon" className="relative">
          <Bell className="w-4 h-4" />
          <span className="absolute top-1.5 right-1.5 w-2 h-2 bg-primary rounded-full" />
        </Button>

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              className="flex items-center gap-2.5 rounded-lg px-2 py-1.5 hover:bg-muted transition-colors focus:outline-none"
            >
              <Avatar className="h-8 w-8">
                <AvatarFallback>{initials || "ED"}</AvatarFallback>
              </Avatar>
              <div className="hidden md:block text-left">
                <p className="text-sm font-semibold leading-none">{displayName}</p>
                <p className="text-xs text-muted-foreground mt-0.5">{displayRole}</p>
              </div>
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-52">
            <DropdownMenuLabel>My Account</DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => navigate("/user-settings")}>
              Profile settings
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              className="text-destructive focus:text-destructive focus:bg-destructive/10"
              onClick={() => navigate("/login")}
            >
              Log out
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  )
}
