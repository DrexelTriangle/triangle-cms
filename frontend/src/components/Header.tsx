import { Button, LinkButton, Popover } from "@cloudflare/kumo"
import { ArrowSquareOutIcon, GearIcon, ShieldIcon, SignOutIcon } from "@phosphor-icons/react"
import { useState } from "react"
import { Link } from "react-router-dom"

function Header() {
  const [userMenuOpen, setUserMenuOpen] = useState(false)
  const user = {
    name: "Delta Admin",
    email: "admin@thetriangle.org",
  }
  const initials = (user.name[0] ?? "D").toUpperCase()

  return (
    <header className="triangle-header">
      <div className="triangle-header-actions">
        <LinkButton className="triangle-header-option" external href="https://www.thetriangle.org" size="sm" variant="ghost">
          <ArrowSquareOutIcon className="triangle-header-link-icon" />
          View Site
        </LinkButton>

        <Popover onOpenChange={setUserMenuOpen} open={userMenuOpen}>
          <Popover.Trigger asChild>
            <Button className="triangle-user-trigger" size="sm" variant="ghost">
              <div className="triangle-user-avatar">{initials}</div>
              <span className="triangle-user-name">{user.name}</span>
            </Button>
          </Popover.Trigger>

          <Popover.Content align="end" className="triangle-user-menu">
            <div className="triangle-user-menu-info">
              <div className="triangle-user-menu-name">{user.name}</div>
              <div className="triangle-user-menu-email">{user.email}</div>
            </div>

            <div className="triangle-user-menu-actions">
              <Link className="triangle-user-menu-link" onClick={() => setUserMenuOpen(false)} to="/settings/security">
                <ShieldIcon className="triangle-user-menu-icon" />
                Security Settings
              </Link>

              <Link className="triangle-user-menu-link" onClick={() => setUserMenuOpen(false)} to="/user-settings">
                <GearIcon className="triangle-user-menu-icon" />
                User Settings
              </Link>

              <button className="triangle-user-menu-logout" type="button">
                <SignOutIcon className="triangle-user-menu-icon" />
                Log out
              </button>
            </div>
          </Popover.Content>
        </Popover>
      </div>
    </header>
  )
}

export default Header
