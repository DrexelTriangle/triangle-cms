import * as React from "react"
import { CalendarClock, X } from "lucide-react"
import { cn } from "@/lib/utils"

export type DateTimeFieldProps = Omit<React.InputHTMLAttributes<HTMLInputElement>, "type" | "onChange"> & {
  label?: string
  hint?: string
  /** Shown in destructive styling under the field; also marks the input invalid. */
  error?: string
  /** Renders a clear button once a value is set. */
  clearable?: boolean
  value: string
  onChange: (value: string) => void
}

// A native <input type="datetime-local"> styled to match the app's Input.
//
// Left alone, the control is the odd one out on every form it appears on: the
// browser draws it in its own font at its own height, and the picker button is
// a grey glyph that ignores the theme entirely. So the built-in indicator is
// hidden and replaced with a real button that calls showPicker(), which keeps
// the native picker (and native keyboard entry) while putting the trigger under
// our own styling.
const DateTimeField = React.forwardRef<HTMLInputElement, DateTimeFieldProps>(
  ({ label, hint, error, clearable, value, onChange, className, disabled, id, ...props }, forwardedRef) => {
    const generatedId = React.useId()
    const fieldId = id ?? generatedId
    const inputRef = React.useRef<HTMLInputElement | null>(null)

    const setRefs = (node: HTMLInputElement | null) => {
      inputRef.current = node
      if (typeof forwardedRef === "function") forwardedRef(node)
      else if (forwardedRef) forwardedRef.current = node
    }

    const openPicker = () => {
      const input = inputRef.current
      if (!input || disabled) return
      // showPicker throws if the browser blocks it (no user gesture, or the
      // control is not focusable); focusing is a fine fallback since the field
      // is still typeable.
      try {
        input.showPicker()
      } catch {
        input.focus()
      }
    }

    return (
      <div className="flex flex-col gap-1.5">
        {label && (
          <label htmlFor={fieldId} className="text-sm font-medium text-foreground">
            {label}
          </label>
        )}
        <div className="relative">
          <input
            {...props}
            ref={setRefs}
            id={fieldId}
            type="datetime-local"
            value={value}
            disabled={disabled}
            aria-invalid={error ? true : undefined}
            onChange={(e) => onChange(e.target.value)}
            className={cn(
              "flex h-10 w-full rounded-lg border border-input bg-background font-sans text-sm text-foreground",
              // Room on the right for the trigger (and the clear button beside it).
              "pl-3 py-2",
              clearable && value ? "pr-16" : "pr-10",
              "ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1",
              "disabled:cursor-not-allowed disabled:opacity-50",
              // The app is light-themed; saying so stops the browser painting
              // the field's internals from the OS preference instead.
              "[color-scheme:light]",
              // Drop the browser's own picker glyph; ours replaces it. Removing
              // it rather than covering it keeps clicking a segment (the month,
              // the hour) working the way it does natively.
              "[&::-webkit-calendar-picker-indicator]:hidden",
              // An empty field otherwise shows near-black "mm/dd/yyyy" that
              // reads as filled in; muted matches every other placeholder.
              !value && "text-muted-foreground",
              error && "border-destructive focus-visible:ring-destructive/40",
              className,
            )}
          />
          <div className="pointer-events-none absolute inset-y-0 right-0 flex items-center gap-0.5 pr-1.5">
            {clearable && value && (
              <button
                type="button"
                onClick={() => onChange("")}
                disabled={disabled}
                aria-label={label ? `Clear ${label.toLowerCase()}` : "Clear date"}
                className="pointer-events-auto rounded-md p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            )}
            <button
              type="button"
              tabIndex={-1}
              onClick={openPicker}
              disabled={disabled}
              aria-label={label ? `Open ${label.toLowerCase()} picker` : "Open date picker"}
              className="pointer-events-auto rounded-md p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-primary"
            >
              <CalendarClock className="h-4 w-4" />
            </button>
          </div>
        </div>
        {error ? (
          <p className="text-xs text-destructive">{error}</p>
        ) : (
          hint && <p className="text-xs text-muted-foreground">{hint}</p>
        )}
      </div>
    )
  },
)
DateTimeField.displayName = "DateTimeField"

export { DateTimeField }
