import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import * as React from "react";

import { cn } from "@/lib/utils";

const badgeVariants = cva(
	"inline-flex items-center justify-center rounded-full border px-2 py-0.5 text-xs leading-4 font-medium w-fit whitespace-nowrap shrink-0 [&>svg]:size-3 gap-1 [&>svg]:pointer-events-none focus-visible:ring-2 focus-visible:ring-(--focus) aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive transition-[color,background-color,box-shadow] overflow-hidden",
	{
		variants: {
			variant: {
				default: "border-primary/25 bg-primary/10 text-primary [a&]:hover:bg-primary/90 [a&]:hover:text-primary-foreground",
				secondary: "border-transparent bg-secondary text-secondary-foreground [a&]:hover:bg-secondary/90",
				destructive:
					"border-danger/30 bg-danger-soft text-danger-ink [a&]:hover:bg-destructive/90 [a&]:hover:text-white",
				outline: "border-border text-foreground [a&]:hover:bg-accent [a&]:hover:text-accent-foreground",
				success: "border-success/30 bg-success-soft text-success-ink [a&]:hover:bg-success/20",
				warning: "border-warning/40 bg-warning-soft text-warning-ink [a&]:hover:bg-warning/20",
				info: "border-info/30 bg-info-soft text-info-ink [a&]:hover:bg-info/20",
				neutral: "border-border bg-muted text-muted-foreground [a&]:hover:bg-muted/80",
			},
		},
		defaultVariants: {
			variant: "default",
		},
	},
);

/** The five meaning tones every local pill maps to (X-2); `tone` wins over `variant`. */
export type BadgeTone = "neutral" | "info" | "success" | "warning" | "danger";
const TONE_VARIANT: Record<BadgeTone, NonNullable<VariantProps<typeof badgeVariants>["variant"]>> = {
	neutral: "neutral",
	info: "info",
	success: "success",
	warning: "warning",
	danger: "destructive",
};

function Badge({
	className,
	variant,
	tone,
	asChild = false,
	...props
}: React.ComponentProps<"span"> & VariantProps<typeof badgeVariants> & { asChild?: boolean; tone?: BadgeTone }) {
	const Comp = asChild ? Slot : "span";

	return <Comp data-slot="badge" className={cn(badgeVariants({ variant: tone ? TONE_VARIANT[tone] : variant }), className)} {...props} {...(tone ? { "data-tone": tone } : {})} />;
}

export { Badge, badgeVariants };