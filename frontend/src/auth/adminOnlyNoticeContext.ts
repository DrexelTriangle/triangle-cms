import { createContext, useContext } from "react"

export type AdminOnlyNoticeValue = {
  /** Raise the "admin only" dialog. Safe to call repeatedly; it is idempotent
   *  while the dialog is already open. */
  show: () => void
}

export const AdminOnlyNoticeContext = createContext<AdminOnlyNoticeValue | null>(null)

/**
 * Access to the shared "admin only" dialog.
 *
 * Unlike useSessionAuth this does not throw when no provider is mounted -- it
 * degrades to a no-op. useApiFetch calls it on every request, and a missing
 * provider should not take down a page (or a test) that never triggers a 403.
 */
export function useAdminOnlyNotice(): AdminOnlyNoticeValue {
  return useContext(AdminOnlyNoticeContext) ?? { show: () => undefined }
}
