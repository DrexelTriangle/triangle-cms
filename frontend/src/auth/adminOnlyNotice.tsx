import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { ShieldAlert } from "lucide-react"
import { AdminOnlyNoticeContext, type AdminOnlyNoticeValue } from "./adminOnlyNoticeContext"

/**
 * Owns the single "admin only" dialog for the whole app.
 *
 * The CMS has exactly two roles, editor and admin, so every 403 a signed-in
 * user can actually receive comes from middleware.RequireAdmin. That makes a
 * blanket "this is admin only" explanation accurate, and it beats surfacing the
 * server's bare "forbidden" in whatever error banner the page happens to have.
 * useApiFetch raises this; pages do not need to know about it.
 */
export function AdminOnlyNoticeProvider({ children }: { children: React.ReactNode }) {
  const [isOpen, setIsOpen] = useState(false)
  const dismissRef = useRef<HTMLButtonElement>(null)

  const close = useCallback(() => setIsOpen(false), [])
  const value = useMemo<AdminOnlyNoticeValue>(() => ({ show: () => setIsOpen(true) }), [])

  useEffect(() => {
    if (!isOpen) return
    dismissRef.current?.focus()
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") close()
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [isOpen, close])

  return (
    <AdminOnlyNoticeContext.Provider value={value}>
      {children}
      {isOpen && (
        <div
          className="fixed inset-0 z-[100] flex items-center justify-center bg-black/40 p-4"
          onClick={close}
          role="presentation"
        >
          <div
            aria-labelledby="admin-only-title"
            aria-modal="true"
            className="w-full max-w-sm rounded-xl border border-border bg-background p-6 shadow-lg flex flex-col gap-4"
            onClick={(e) => e.stopPropagation()}
            role="dialog"
          >
            <div className="flex items-start gap-3">
              <span className="rounded-lg bg-muted p-2 text-muted-foreground">
                <ShieldAlert className="w-5 h-5" aria-hidden="true" />
              </span>
              <div className="flex flex-col gap-1">
                <h2 className="text-base font-semibold text-foreground" id="admin-only-title">
                  Admin only
                </h2>
                <p className="text-sm text-muted-foreground">
                  This action requires an admin account. Nothing was saved.
                </p>
              </div>
            </div>
            <button
              className="self-end px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors"
              onClick={close}
              ref={dismissRef}
              type="button"
            >
              Got it
            </button>
          </div>
        </div>
      )}
    </AdminOnlyNoticeContext.Provider>
  )
}
