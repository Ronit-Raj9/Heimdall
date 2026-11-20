import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
	return twMerge(clsx(inputs));
}
/**
 * A Radix Select value that stays controlled from the first render: an empty string shows the
 * placeholder, so a picker filled in by a later response never switches from uncontrolled.
 */
export function controlledSelectValue(value: string | null | undefined): string {
	return value ?? "";
}
