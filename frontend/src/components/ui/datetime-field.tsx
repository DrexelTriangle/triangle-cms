import * as React from "react"
import * as PopoverPrimitive from "@radix-ui/react-popover"
import { CalendarClock, ChevronLeft, ChevronRight, X } from "lucide-react"
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

// The form value, and what <input type="datetime-local"> speaks: local wall
// time, no zone.
const VALUE_PATTERN = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/

// Picking a day on an empty field has to commit to some time. Midnight would
// silently schedule a poll for the small hours of the chosen day, which is
// never what someone clicking a date means; 09:00 reads as "that morning".
const DEFAULT_HOUR = 9
const DEFAULT_MINUTE = 0

type Parts = { year: number; month: number; day: number; hour: number; minute: number }

const pad = (n: number) => String(n).padStart(2, "0")

function parseValue(value: string): Parts | null {
  const match = VALUE_PATTERN.exec(value)
  if (!match) return null
  return {
    year: Number(match[1]),
    month: Number(match[2]) - 1,
    day: Number(match[3]),
    hour: Number(match[4]),
    minute: Number(match[5]),
  }
}

const toValue = (p: Parts) => `${p.year}-${pad(p.month + 1)}-${pad(p.day)}T${pad(p.hour)}:${pad(p.minute)}`
const dayKey = (year: number, month: number, day: number) => `${year}-${pad(month + 1)}-${pad(day)}`
const keyOf = (d: Date) => dayKey(d.getFullYear(), d.getMonth(), d.getDate())

// 2024-01-07 was a Sunday, so seven days from it spell a week in the viewer's
// own locale rather than in hardcoded English.
const WEEKDAYS = (() => {
  const fmt = new Intl.DateTimeFormat(undefined, { weekday: "short" })
  return Array.from({ length: 7 }, (_, i) => fmt.format(new Date(2024, 0, 7 + i)))
})()

const monthLabel = (date: Date) =>
  new Intl.DateTimeFormat(undefined, { month: "long", year: "numeric" }).format(date)
const fullDayLabel = (date: Date) =>
  new Intl.DateTimeFormat(undefined, { weekday: "long", month: "long", day: "numeric", year: "numeric" }).format(date)

const NAV_BUTTON =
  "inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"

type CalendarProps = {
  selected: Parts | null
  min?: string
  max?: string
  onSelect: (year: number, month: number, day: number) => void
}

// A month grid. Rolled by hand rather than pulled in as a dependency: the
// native popup is unstyleable browser chrome, which is the whole reason we are
// here, and a calendar we do not own would only move that problem behind
// someone else's class names.
function Calendar({ selected, min, max, onSelect }: CalendarProps) {
  const today = React.useMemo(() => new Date(), [])
  const [view, setView] = React.useState(
    () => new Date(selected?.year ?? today.getFullYear(), selected?.month ?? today.getMonth(), 1),
  )
  const [focused, setFocused] = React.useState<Date>(
    () =>
      selected
        ? new Date(selected.year, selected.month, selected.day)
        : new Date(today.getFullYear(), today.getMonth(), today.getDate()),
  )
  // Only steal focus when the user is actually arrowing around the grid --
  // otherwise opening the popover would rip focus off the trigger.
  const keyboardNav = React.useRef(false)
  const cellRefs = React.useRef(new Map<string, HTMLButtonElement>())

  React.useEffect(() => {
    if (!keyboardNav.current) return
    keyboardNav.current = false
    cellRefs.current.get(keyOf(focused))?.focus()
  }, [focused])

  const minKey = min ? min.slice(0, 10) : undefined
  const maxKey = max ? max.slice(0, 10) : undefined
  const isDisabled = (date: Date) => {
    const key = keyOf(date)
    return (!!minKey && key < minKey) || (!!maxKey && key > maxKey)
  }

  // Six rows always, so the popover does not resize as months change height.
  const firstOfMonth = new Date(view.getFullYear(), view.getMonth(), 1)
  const days = Array.from(
    { length: 42 },
    (_, i) => new Date(view.getFullYear(), view.getMonth(), 1 - firstOfMonth.getDay() + i),
  )

  const moveFocus = (next: Date) => {
    keyboardNav.current = true
    setFocused(next)
    if (next.getMonth() !== view.getMonth() || next.getFullYear() !== view.getFullYear()) {
      setView(new Date(next.getFullYear(), next.getMonth(), 1))
    }
  }

  const onKeyDown = (e: React.KeyboardEvent) => {
    const shift: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 }
    if (e.key in shift) {
      e.preventDefault()
      const next = new Date(focused)
      next.setDate(next.getDate() + shift[e.key])
      moveFocus(next)
      return
    }
    if (e.key === "PageUp" || e.key === "PageDown") {
      e.preventDefault()
      const next = new Date(focused)
      next.setMonth(next.getMonth() + (e.key === "PageUp" ? -1 : 1))
      moveFocus(next)
      return
    }
    if (e.key === "Home" || e.key === "End") {
      e.preventDefault()
      const next = new Date(focused)
      next.setDate(e.key === "Home" ? 1 : new Date(focused.getFullYear(), focused.getMonth() + 1, 0).getDate())
      moveFocus(next)
    }
  }

  const shiftMonth = (delta: number) =>
    setView((current) => new Date(current.getFullYear(), current.getMonth() + delta, 1))

  const selectedKey = selected ? dayKey(selected.year, selected.month, selected.day) : ""
  const todayKey = keyOf(today)
  const focusedKey = keyOf(focused)

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between px-1">
        <button type="button" onClick={() => shiftMonth(-1)} aria-label="Previous month" className={NAV_BUTTON}>
          <ChevronLeft className="h-4 w-4" />
        </button>
        <span aria-live="polite" className="text-sm font-medium text-foreground">
          {monthLabel(view)}
        </span>
        <button type="button" onClick={() => shiftMonth(1)} aria-label="Next month" className={NAV_BUTTON}>
          <ChevronRight className="h-4 w-4" />
        </button>
      </div>

      <div role="grid" onKeyDown={onKeyDown} className="flex flex-col gap-1">
        <div role="row" className="grid grid-cols-7">
          {WEEKDAYS.map((day) => (
            <span
              key={day}
              role="columnheader"
              aria-label={day}
              className="flex h-7 items-center justify-center text-[11px] font-medium uppercase text-muted-foreground"
            >
              {day.slice(0, 2)}
            </span>
          ))}
        </div>
        <div className="grid grid-cols-7 gap-0.5">
          {days.map((date) => {
            const key = keyOf(date)
            const outside = date.getMonth() !== view.getMonth()
            const isSelected = key === selectedKey
            const disabled = isDisabled(date)
            return (
              <button
                key={key}
                type="button"
                role="gridcell"
                ref={(node) => {
                  if (node) cellRefs.current.set(key, node)
                  else cellRefs.current.delete(key)
                }}
                tabIndex={key === focusedKey ? 0 : -1}
                disabled={disabled}
                aria-selected={isSelected}
                aria-label={fullDayLabel(date)}
                onFocus={() => setFocused(date)}
                onClick={() => onSelect(date.getFullYear(), date.getMonth(), date.getDate())}
                className={cn(
                  "flex h-8 w-8 items-center justify-center rounded-md text-sm transition-colors",
                  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                  "disabled:pointer-events-none disabled:opacity-30",
                  outside ? "text-muted-foreground/50" : "text-foreground",
                  !isSelected && "hover:bg-muted",
                  isSelected && "bg-primary text-primary-foreground hover:bg-primary/90",
                  // Today is a reference point, not a selection -- an outline
                  // says "you are here" without competing with the filled cell.
                  !isSelected && key === todayKey && "ring-1 ring-inset ring-primary/40 font-medium",
                )}
              >
                {date.getDate()}
              </button>
            )
          })}
        </div>
      </div>
    </div>
  )
}

// A native <input type="datetime-local"> for typing, with our own popover in
// place of the browser's picker.
//
// The native popup is browser chrome: it renders outside the page, so no CSS of
// ours reaches it, and in Firefox it offers a calendar and no time at all. The
// field itself stays native -- it is locale-aware, screen-reader-correct and
// keyboard-typeable for free, and none of that is worth reimplementing. Only
// the popup is ours.
const DateTimeField = React.forwardRef<HTMLInputElement, DateTimeFieldProps>(
  ({ label, hint, error, clearable, value, onChange, className, disabled, id, min, max, ...props }, forwardedRef) => {
    const generatedId = React.useId()
    const fieldId = id ?? generatedId
    const [open, setOpen] = React.useState(false)
    const inputRef = React.useRef<HTMLInputElement | null>(null)

    const setRefs = (node: HTMLInputElement | null) => {
      inputRef.current = node
      if (typeof forwardedRef === "function") forwardedRef(node)
      else if (forwardedRef) forwardedRef.current = node
    }

    const parsed = parseValue(value)
    const timeValue = parsed ? `${pad(parsed.hour)}:${pad(parsed.minute)}` : ""

    const selectDay = (year: number, month: number, day: number) => {
      onChange(
        toValue({
          year,
          month,
          day,
          hour: parsed?.hour ?? DEFAULT_HOUR,
          minute: parsed?.minute ?? DEFAULT_MINUTE,
        }),
      )
    }

    // A time with no date yet means today at that time; the alternative is
    // typing a time that silently does nothing.
    const setTime = (next: string) => {
      const [hour, minute] = next.split(":")
      if (hour === undefined || minute === undefined || next === "") return
      const base = parsed ?? {
        year: new Date().getFullYear(),
        month: new Date().getMonth(),
        day: new Date().getDate(),
        hour: DEFAULT_HOUR,
        minute: DEFAULT_MINUTE,
      }
      onChange(toValue({ ...base, hour: Number(hour), minute: Number(minute) }))
    }

    const selectToday = () => {
      const now = new Date()
      selectDay(now.getFullYear(), now.getMonth(), now.getDate())
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
            min={min}
            max={max}
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
            <PopoverPrimitive.Root open={open} onOpenChange={setOpen}>
              <PopoverPrimitive.Trigger asChild>
                <button
                  type="button"
                  tabIndex={-1}
                  disabled={disabled}
                  aria-label={label ? `Open ${label.toLowerCase()} picker` : "Open date picker"}
                  className="pointer-events-auto rounded-md p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-primary data-[state=open]:bg-muted data-[state=open]:text-primary"
                >
                  <CalendarClock className="h-4 w-4" />
                </button>
              </PopoverPrimitive.Trigger>
              <PopoverPrimitive.Portal>
                <PopoverPrimitive.Content
                  align="end"
                  sideOffset={6}
                  // The field is inside a <form>; Enter on a day cell must pick
                  // a date, not submit the poll.
                  onKeyDown={(e) => {
                    if (e.key === "Enter") e.stopPropagation()
                  }}
                  className={cn(
                    "pointer-events-auto z-50 w-auto rounded-lg border border-border bg-card p-3 shadow-lg",
                    "data-[state=open]:animate-in data-[state=closed]:animate-out",
                    "data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0",
                    "data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95",
                  )}
                >
                  <Calendar selected={parsed} min={min?.toString()} max={max?.toString()} onSelect={selectDay} />
                  <div className="mt-3 flex items-center gap-2 border-t border-border pt-3">
                    <label htmlFor={`${fieldId}-time`} className="text-xs font-medium text-muted-foreground">
                      Time
                    </label>
                    <input
                      id={`${fieldId}-time`}
                      type="time"
                      value={timeValue}
                      onChange={(e) => setTime(e.target.value)}
                      className={cn(
                        "h-8 flex-1 rounded-md border border-input bg-background px-2 text-sm text-foreground",
                        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                        "[color-scheme:light] [&::-webkit-calendar-picker-indicator]:hidden",
                      )}
                    />
                  </div>
                  <div className="mt-2 flex items-center justify-between">
                    <button
                      type="button"
                      onClick={selectToday}
                      className="rounded-md px-2 py-1 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                    >
                      Today
                    </button>
                    <button
                      type="button"
                      onClick={() => setOpen(false)}
                      className="rounded-md bg-primary px-3 py-1 text-xs font-medium text-primary-foreground transition-colors hover:bg-primary/90"
                    >
                      Done
                    </button>
                  </div>
                </PopoverPrimitive.Content>
              </PopoverPrimitive.Portal>
            </PopoverPrimitive.Root>
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
