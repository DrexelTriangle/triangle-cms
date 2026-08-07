// Shared styling for the article status chips so the dashboard and the
// articles list always render the same colors for the same status.
const STATUS_CHIP_COLORS: Record<string, string> = {
  published: "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400",
  scheduled: "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-300",
  draft: "bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-400",
}

const FALLBACK_CHIP_COLOR = "bg-slate-200 text-slate-700 dark:bg-slate-700/40 dark:text-slate-200"

export const ARTICLE_STATUS_CHIP_BASE =
  "inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium"

export function articleStatusChipClass(status: string) {
  const color = STATUS_CHIP_COLORS[status.toLowerCase()] ?? FALLBACK_CHIP_COLOR
  return `${ARTICLE_STATUS_CHIP_BASE} ${color}`
}
