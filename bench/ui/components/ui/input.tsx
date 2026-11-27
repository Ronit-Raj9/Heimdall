// Copied from ui/components/ui/input.tsx with the same styling tokens. The copy-button
// variant is left out because it needs ui/'s clipboard hook.
import * as React from "react";

import { cn } from "@/lib/utils";

export interface InputProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "size"> {
	inputClassName?: string;
	/**
	 * Width rule (X-100): "number" for counts, budgets and caps (96 px), "id" for ids and names
	 * (stops at 448 px), "full" (default) for everything else.
	 */
	size?: "number" | "id" | "full" | number;
}

const INPUT_WIDTH: Record<"number" | "id" | "full", string> = {
	number: "w-24",
	id: "w-full max-w-md",
	full: "w-full",
};

export const Input = React.forwardRef<HTMLInputElement, InputProps>(({ className, type, inputClassName, size = "full", ...props }, ref) => {
	// A number is the native size attribute (forwarded props); a word is the width rule.
	const width = typeof size === "number" ? "w-full" : INPUT_WIDTH[size];
	const nativeSize = typeof size === "number" ? size : undefined;
	return (
		<input
			type={type}
			ref={ref}
			data-slot="input"
			className={cn(
				"file:text-foreground placeholder:text-neutral-500 selection:bg-primary selection:text-primary-foreground dark:bg-card border-input focus-visible:border-primary focus-visible:ring-(--focus)/40 flex h-(--control-h) min-w-0 rounded-md border bg-card px-3 py-1 text-base shadow-xs transition-[color,box-shadow,border-color] focus-visible:ring-2 outline-none file:inline-flex file:h-7 file:border-0 file:bg-transparent file:text-sm file:font-medium disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 md:text-sm",
				"aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive",
				width,
				className,
				inputClassName,
			)}
			size={nativeSize}
			{...props}
		/>
	);
});

Input.displayName = "Input";