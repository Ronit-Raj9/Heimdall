import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import * as React from "react";

import { cn } from "@/lib/utils";

const badgeVariants = cva(
	"inline-flex items-center justify-center rounded-full border px-2 py-0.5 text-xs leading-4 font-medium w-fit whitespace-nowrap shrink-0 [&>svg]:size-3 gap-1 [&>svg]:pointer-events-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px] aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive transition-[color,background-color,box-shadow] overflow-hidden",
	{
		variants: {
			variant: {
				default: "border-primary/25 bg-primary/10 text-primary [a&]:hover:bg-primary/90 [a&]:hover:text-primary-foreground",
				secondary: "border-transparent bg-secondary text-secondary-foreground [a&]:hover:bg-secondary/90",
				destructive:
					"border-chart-error/30 bg-chart-error/10 text-chart-error-ink [a&]:hover:bg-destructive/90 [a&]:hover:text-white focus-visible:ring-destructive/20 dark:focus-visible:ring-destructive/40",
				outline: "border-border/80 text-foreground [a&]:hover:bg-accent [a&]:hover:text-accent-foreground",
				success: "border-chart-success/30 bg-chart-success/10 text-chart-success-ink [a&]:hover:bg-chart-success/20",
				warning: "border-chart-warning/40 bg-chart-warning/10 text-chart-warning-ink [a&]:hover:bg-chart-warning/20",
				info: "border-chart-info/30 bg-chart-info/10 text-chart-info-ink [a&]:hover:bg-chart-info/20",
				neutral: "border-border/80 bg-muted text-muted-foreground [a&]:hover:bg-muted/80",
			},
		},
		defaultVariants: {
			variant: "default",
		},
	},
);

function Badge({
	className,
	variant,
	asChild = false,
	...props
}: React.ComponentProps<"span"> & VariantProps<typeof badgeVariants> & { asChild?: boolean }) {
	const Comp = asChild ? Slot : "span";

	return <Comp data-slot="badge" className={cn(badgeVariants({ variant }), className)} {...props} />;
}

export { Badge, badgeVariants };