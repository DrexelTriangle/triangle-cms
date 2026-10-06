import { X } from "lucide-react"
import { useEffect, useId, type ReactNode } from "react"
import { cn } from "@/lib/utils"

type DialogProps = {
  title: string
  onClose: () => void
  children: ReactNode
  className?: string
}

// Modal shell matching MediaPicker: backdrop click and Escape close it.
export default function Dialog({ title, onClose, children, className }: DialogProps) {
  const titleId = useId()
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [onClose])

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" onClick={onClose}>
      <div
        aria-labelledby={titleId}
        aria-modal="true"
        className={cn("flex max-h-[90vh] w-full max-w-lg flex-col gap-4 overflow-y-auto rounded-xl border border-border bg-background p-6", className)}
        onClick={(e) => e.stopPropagation()}
        role="dialog"
      >
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-lg font-semibold text-foreground" id={titleId}>{title}</h2>
          <button aria-label="Close" className="p-1 text-muted-foreground hover:text-foreground" onClick={onClose} type="button">
            <X className="h-5 w-5" />
          </button>
        </div>
        {children}
      </div>
    </div>
  )
}
