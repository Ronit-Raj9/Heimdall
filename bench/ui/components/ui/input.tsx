// Copied from ui/components/ui/input.tsx with the same styling tokens. The copy-button
// variant is left out because it needs ui/'s clipboard hook.
import * as React from "react";

import { cn } from "@/lib/utils";

export interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
	inputClassName?: string;
}

export const Input = React.forwardRef<HTMLInputElement, InputProps>(({ className, type, inputClassName, ...props }, ref) => {
	return (
		<input
			type={type}
			ref={ref}
			data-slot="input"
			className={cn(
				"file:text-foreground placeholder:text-muted-foreground/70 selection:bg-primary selection:text-primary-foreground dark:bg-input/30 border-input focus-visible:border-primary focus-visible:ring-ring/30 flex h-9 w-full min-w-0 rounded-md border bg-card px-3 py-1 text-base shadow-xs transition-[color,box-shadow,border-color] focus-visible:ring-2 outline-none file:inline-flex file:h-7 file:border-0 file:bg-transparent file:text-sm file:font-medium disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 md:text-sm",
				"aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive",
				className,
				inputClassName,
			)}
			{...props}
		/>
	);
});

Input.displayName = "Input";