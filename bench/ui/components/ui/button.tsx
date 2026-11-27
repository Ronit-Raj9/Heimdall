import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import * as React from "react";

import { cn } from "@/lib/utils";
import { Loader2 } from "lucide-react";

const buttonVariants = cva(
	"inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-lg text-sm font-medium transition-[background-color,color,border-color,box-shadow,transform] disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg:not([class*='size-'])]:size-4 shrink-0 [&_svg]:shrink-0 outline-none focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:ring-offset-background aria-invalid:border-destructive active:scale-[0.97] duration-150 ease-(--ease-out)",
	{
		variants: {
			variant: {
				default: "bg-primary text-primary-foreground shadow-xs hover:bg-(--primary-hover) active:bg-(--primary-active) focus-visible:ring-(--focus)",
				destructive:
					"bg-destructive text-white hover:bg-destructive/90 focus-visible:ring-(--focus) dark:bg-destructive/80",
				outline:
					"border border-border bg-card shadow-xs hover:bg-accent hover:text-accent-foreground focus-visible:ring-(--focus) dark:bg-card dark:border-input dark:hover:bg-accent",
				secondary: "bg-secondary text-secondary-foreground hover:bg-secondary/80 focus-visible:ring-(--focus)",
				ghost: "hover:bg-accent hover:text-accent-foreground focus-visible:ring-(--focus) dark:hover:bg-accent/50",
				link: "text-primary underline-offset-4 hover:underline focus-visible:ring-(--focus)",
			},
			size: {
				default: "h-(--control-h) px-3 py-1 has-[>svg]:px-2.5",
				sm: "h-7 gap-1.5 px-2.5 text-[13px] has-[>svg]:px-2",
				lg: "h-10 px-6 has-[>svg]:px-4",
				icon: "size-9",
			},
		},
		defaultVariants: {
			variant: "default",
			size: "default",
		},
	},
);

function Button({
	className,
	variant,
	size,
	asChild = false,
	children,
	isLoading = false,
	dataTestId,
	...props
}: React.ComponentProps<"button"> &
	VariantProps<typeof buttonVariants> & {
		asChild?: boolean;
		isLoading?: boolean;
		dataTestId?: string;
	}) {
	// A busy button keeps its label, so it does not change width, and cannot be pressed twice.
	// With asChild the child is the only node Slot accepts, so the spinner is left out there.
	return (
		<BaseButton
			className={className}
			variant={variant}
			size={size}
			asChild={asChild}
			dataTestId={dataTestId}
			{...props}
			disabled={props.disabled || isLoading}
			aria-busy={isLoading || undefined}
		>
			{isLoading && !asChild ? (
				<>
					<Loader2 className="size-4 animate-spin" aria-hidden="true" />
					{children}
				</>
			) : (
				children
			)}
		</BaseButton>
	);
}

function BaseButton({
	className,
	variant,
	size,
	asChild = false,
	dataTestId,
	...props
}: React.ComponentProps<"button"> &
	VariantProps<typeof buttonVariants> & {
		asChild?: boolean;
		dataTestId?: string;
	}) {
	const Comp = asChild ? Slot : "button";

	return (
		<Comp
			data-slot="button"
			data-testid={dataTestId}
			className={cn(buttonVariants({ variant, size, className }), "cursor-pointer")}
			{...props}
		/>
	);
}

export { Button, buttonVariants };