import * as React from "react";

import { cn } from "@/lib/utils";

function Card({
	className,
	interactive = false,
	...props
}: React.ComponentProps<"div"> & {
	/** Hover lift for cards that open something (a link or a click target). */
	interactive?: boolean;
}) {
	return (
		<div
			data-slot="card"
			data-interactive={interactive || undefined}
			className={cn(
				"bg-card text-card-foreground flex flex-col gap-(--section-gap) rounded-xl border border-border py-(--card-pad) shadow-(--shadow-card) transition-[box-shadow,border-color,transform] duration-200 ease-(--ease-out)",
				interactive && "hover:border-border cursor-pointer hover:-translate-y-0.5 hover:shadow-md motion-reduce:hover:translate-y-0",
				className,
			)}
			{...props}
		/>
	);
}

function CardHeader({ className, ...props }: React.ComponentProps<"div">) {
	return (
		<div
			data-slot="card-header"
			className={cn(
				"@container/card-header grid auto-rows-min grid-rows-[auto_auto] items-start gap-1.5 px-(--card-pad) has-data-[slot=card-action]:grid-cols-[1fr_auto] [.border-b]:pb-(--card-pad)",
				className,
			)}
			{...props}
		/>
	);
}

function CardTitle({ className, ...props }: React.ComponentProps<"div">) {
	return <div data-slot="card-title" className={cn("text-base leading-tight font-semibold tracking-[-0.01em]", className)} {...props} />;
}

function CardDescription({ className, ...props }: React.ComponentProps<"div">) {
	return <div data-slot="card-description" className={cn("text-muted-foreground text-sm", className)} {...props} />;
}

function CardAction({ className, ...props }: React.ComponentProps<"div">) {
	return (
		<div data-slot="card-action" className={cn("col-start-2 row-span-2 row-start-1 self-start justify-self-end", className)} {...props} />
	);
}

function CardContent({ className, ...props }: React.ComponentProps<"div">) {
	return <div data-slot="card-content" className={cn("px-(--card-pad)", className)} {...props} />;
}

function CardFooter({ className, ...props }: React.ComponentProps<"div">) {
	return <div data-slot="card-footer" className={cn("flex items-center px-(--card-pad) [.border-t]:pt-(--card-pad)", className)} {...props} />;
}

export { Card, CardAction, CardContent, CardDescription, CardFooter, CardHeader, CardTitle };