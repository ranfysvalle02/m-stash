// Context + hook for the toast system. Kept separate from the provider component
// so the provider file only exports a component (fast-refresh friendly) and any
// component can pull in useToast without importing the provider's JSX.

import { createContext, useContext } from "react"

export type ToastKind = "success" | "info" | "error"

export interface ToastApi {
  success: (message: string) => void
  info: (message: string) => void
  error: (message: string) => void
}

// useToast is safe to call without a provider (it no-ops), so a component can
// request a toast whether or not its tree is wrapped in a ToastProvider.
const noopToast: ToastApi = { success: () => {}, info: () => {}, error: () => {} }

export const ToastContext = createContext<ToastApi | null>(null)

export function useToast(): ToastApi {
  return useContext(ToastContext) ?? noopToast
}
