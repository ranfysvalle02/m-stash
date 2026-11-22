// A tiny, dependency-free toast system shared by the runner, the vibe panel, and
// the console. Toasts are the crisp replacement for silent copy/save feedback and
// native alerts, giving the whole product a consistent, high-craft confirmation
// surface. The context and useToast hook live in ./toastContext so this file only
// exports the provider component.

import { useCallback, useMemo, useRef, useState } from "react"
import type { ReactNode } from "react"
import { AlertTriangle, Check, Info, X } from "lucide-react"
import { ToastContext } from "./toastContext"
import type { ToastApi, ToastKind } from "./toastContext"
import "./toast.css"

interface ToastItem {
  id: number
  kind: ToastKind
  message: string
}

const TOAST_TTL_MS = 3200

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastItem[]>([])
  const counter = useRef(0)

  const dismiss = useCallback((id: number) => {
    setToasts((all) => all.filter((toast) => toast.id !== id))
  }, [])

  const push = useCallback(
    (kind: ToastKind, message: string) => {
      counter.current += 1
      const id = counter.current
      setToasts((all) => [...all, { id, kind, message }])
      window.setTimeout(() => dismiss(id), TOAST_TTL_MS)
    },
    [dismiss],
  )

  const api = useMemo<ToastApi>(
    () => ({
      success: (message) => push("success", message),
      info: (message) => push("info", message),
      error: (message) => push("error", message),
    }),
    [push],
  )

  return (
    <ToastContext.Provider value={api}>
      {children}
      <div className="toast-stack" aria-live="polite" role="status">
        {toasts.map((toast) => (
          <div key={toast.id} className={`toast toast-${toast.kind}`}>
            {toast.kind === "success" ? (
              <Check aria-hidden="true" size={16} />
            ) : toast.kind === "error" ? (
              <AlertTriangle aria-hidden="true" size={16} />
            ) : (
              <Info aria-hidden="true" size={16} />
            )}
            <span>{toast.message}</span>
            <button type="button" className="toast-close" onClick={() => dismiss(toast.id)} aria-label="Dismiss">
              <X aria-hidden="true" size={14} />
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  )
}
