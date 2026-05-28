import type { AuthProviderProps } from "react-oidc-context"

export const oidcConfig: AuthProviderProps = {
  authority: import.meta.env.VITE_OIDC_AUTHORITY,
  client_id: import.meta.env.VITE_OIDC_CLIENT_ID,
  redirect_uri: import.meta.env.VITE_REDIRECT_URI ?? `${window.location.origin}/auth/callback`,
  scope: "openid profile email",
  post_logout_redirect_uri: window.location.origin,
  automaticSilentRenew: true,
  onSigninCallback: () => {
    window.history.replaceState({}, document.title, window.location.pathname)
  },
}
