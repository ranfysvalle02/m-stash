// A small inline confirm control that replaces blocking window.confirm() dialogs.
// Resting, it shows its label; the first click "arms" it, swapping in Confirm /
// Cancel affordances right where the action lives, so destructive actions stay in
// the flow of the page instead of a native modal. The "icon" variant is for
// compact row actions (e.g. delete buttons); the default "button" variant is for
// labeled actions (rotate, disable, delete app).

import { useState } from "react"
import type { ReactNode } from "react"
import { Check, Trash2, X } from "lucide-react"

interface ConfirmButtonProps {
  children?: ReactNode
  onConfirm: () => void
  confirmLabel?: string
  className?: string
  disabled?: boolean
  variant?: "button" | "icon"
  ariaLabel?: string
  title?: string
}

export function ConfirmButton({
  children,
  onConfirm,
  confirmLabel = "Confirm",
  className,
  disabled,
  variant = "button",
  ariaLabel,
  title,
}: ConfirmButtonProps) {
  const [armed, setArmed] = useState(false)

  if (!armed) {
    return (
      <button
        type="button"
        className={className ?? (variant === "icon" ? "icon-button danger" : "button secondary danger-button")}
        disabled={disabled}
        aria-label={ariaLabel}
        title={title}
        onClick={() => setArmed(true)}
      >
        {children ?? (variant === "icon" ? <Trash2 aria-hidden="true" size={16} /> : null)}
      </button>
    )
  }

  const confirm = () => {
    setArmed(false)
    onConfirm()
  }

  if (variant === "icon") {
    return (
      <span className="confirm-inline icon">
        <button type="button" className="icon-button danger" aria-label={`${confirmLabel} — confirm`} title="Confirm" onClick={confirm}>
          <Check aria-hidden="true" size={16} />
        </button>
        <button type="button" className="icon-button" aria-label="Cancel" title="Cancel" onClick={() => setArmed(false)}>
          <X aria-hidden="true" size={16} />
        </button>
      </span>
    )
  }

  return (
    <span className="confirm-inline">
      <button type="button" className="button secondary danger-button small-button" disabled={disabled} onClick={confirm}>
        <Check aria-hidden="true" size={14} /> {confirmLabel}
      </button>
      <button type="button" className="button secondary small-button" onClick={() => setArmed(false)}>
        Cancel
      </button>
    </span>
  )
}
