import { useNavigate } from "react-router-dom"
import { useSessionAuth } from "../auth/sessionAuthContext"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
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
  const { user, logout } = useSessionAuth()
  const displayName = String(user?.name ?? user?.email ?? "Editor")
  const initials = displayName.split(" ").map((part) => part[0] ?? "").join("").slice(0, 2).toUpperCase()
  const displayRole = user?.role ? user.role.charAt(0).toUpperCase() + user.role.slice(1) : "Editor"

  return (
    <header className="h-16 flex items-center justify-between px-6 bg-card border-b border-border shrink-0">
      <div className="flex items-center gap-3 ml-auto">
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
              onClick={async () => {
                await logout()
                navigate("/login", { replace: true })
              }}
            >
              Log out
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  )
}
