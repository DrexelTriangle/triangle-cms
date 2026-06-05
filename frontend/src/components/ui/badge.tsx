import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"

const badgeVariants = cva(
  "inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-semibold transition-colors",
  {
    variants: {
      variant: {
        default: "bg-primary/10 text-primary",
        secondary: "bg-secondary text-secondary-foreground",
        destructive: "bg-destructive/10 text-destructive",
        success: "bg-success/10 text-success",
        outline: "border border-border text-foreground",
      },
    },
    defaultVariants: { variant: "default" },
  },
)

export interface BadgeProps extends React.HTMLAttributes<HTMLDivElement>, VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, ...props }: BadgeProps) {
  return <div className={cn(badgeVariants({ variant }), className)} {...props} />
}

export { Badge }
