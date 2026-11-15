import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import * as React from "react";

import { cn } from "@/lib/utils";

const badgeVariants = cva(
	"inline-flex items-center justify-center rounded-[5px] border px-1.5 py-0.5 text-xs leading-4 font-medium w-fit whitespace-nowrap shrink-0 [&>svg]:size-3 gap-1 [&>svg]:pointer-events-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px] aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive transition-[color,box-shadow] overflow-hidden",
	{
		variants: {
			variant: {
				default: "border-primary/35 bg-primary/10 text-primary [a&]:hover:bg-primary/90 [a&]:hover:text-primary-foreground",
				secondary: "border-transparent bg-secondary text-secondary-foreground [a&]:hover:bg-secondary/90",
				destructive:
					"border-chart-error/50 bg-chart-error/10 text-chart-error-ink [a&]:hover:bg-destructive/90 [a&]:hover:text-white focus-visible:ring-destructive/20 dark:focus-visible:ring-destructive/40",
				outline: "text-foreground [a&]:hover:bg-accent [a&]:hover:text-accent-foreground",
				success: "border-chart-success/50 bg-chart-success/10 text-chart-success-ink [a&]:hover:bg-chart-success/20",
				warning: "border-chart-warning/60 bg-chart-warning/10 text-chart-warning-ink [a&]:hover:bg-chart-warning/20",
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