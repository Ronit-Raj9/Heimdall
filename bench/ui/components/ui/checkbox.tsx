import { cn } from "@/lib/utils";
import { useId, type ReactNode } from "react";

/**
 * The shared checkbox and radio group (M4-38, M1-38). A native input keeps keyboard and screen
 * reader behaviour; the whole row is the label, at least 24 px high, so the hit area is not the
 * 16 px box alone; keyboard focus shows the token focus ring on the box.
 */
const boxClass =
	"accent-primary mt-0.5 size-4 shrink-0 cursor-pointer rounded-sm focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-(--focus) disabled:cursor-not-allowed";
const rowClass = "flex min-h-6 items-start gap-2 py-0.5 text-sm";

export interface CheckboxProps {
	checked: boolean;
	onChange(checked: boolean): void;
	label: ReactNode;
	/** A muted second line under the label. */
	hint?: ReactNode;
	disabled?: boolean;
	/** Half-checked, for a "select all" over a partly picked list. */
	indeterminate?: boolean;
	name?: string;
	className?: string;
	testId?: string;
}

export function Checkbox({ checked, onChange, label, hint, disabled, indeterminate, name, className, testId }: CheckboxProps) {
	const hintId = useId();
	return (
		<label
			className={cn(rowClass, disabled ? "cursor-not-allowed opacity-60" : "cursor-pointer", className)}
			data-testid={testId ? `${testId}-row` : undefined}
		>
			<input
				type="checkbox"
				name={name}
				checked={checked}
				disabled={disabled}
				ref={(el) => {
					if (el) el.indeterminate = Boolean(indeterminate);
				}}
				aria-describedby={hint ? hintId : undefined}
				onChange={(e) => onChange(e.target.checked)}
				className={boxClass}
				data-testid={testId}
			/>
			<span className="flex min-w-0 flex-col">
				<span>{label}</span>
				{hint && (
					<span id={hintId} className="text-muted-foreground text-xs">
						{hint}
					</span>
				)}
			</span>
		</label>
	);
}

export interface RadioItem<T extends string = string> {
	value: T;
	label: ReactNode;
	hint?: ReactNode;
	disabled?: boolean;
	testId?: string;
}

export interface RadioGroupProps<T extends string = string> {
	value: T | null;
	onChange(value: T): void;
	items: readonly RadioItem<T>[];
	/** The group name (fieldset legend). */
	label: string;
	/** Hide the legend visually; it stays the accessible name. */
	hideLabel?: boolean;
	/** "column" (default) or "row" (wraps on a narrow screen). */
	direction?: "column" | "row";
	disabled?: boolean;
	className?: string;
	testId?: string;
}

export function RadioGroup<T extends string = string>({
	value,
	onChange,
	items,
	label,
	hideLabel,
	direction = "column",
	disabled,
	className,
	testId,
}: RadioGroupProps<T>) {
	const name = useId();
	return (
		<fieldset className={cn("flex min-w-0 flex-col gap-1", className)} disabled={disabled} data-testid={testId}>
			<legend className={cn("mb-1 text-sm font-medium", hideLabel && "sr-only")}>{label}</legend>
			<div className={cn("flex", direction === "row" ? "flex-row flex-wrap gap-x-4 gap-y-1" : "flex-col gap-0.5")}>
				{items.map((item) => (
					<label key={item.value} className={cn(rowClass, item.disabled || disabled ? "cursor-not-allowed opacity-60" : "cursor-pointer")}>
						<input
							type="radio"
							name={name}
							value={item.value}
							checked={value === item.value}
							disabled={item.disabled}
							onChange={() => onChange(item.value)}
							className={boxClass}
							data-testid={item.testId ?? (testId ? `${testId}-${item.value}` : undefined)}
						/>
						<span className="flex min-w-0 flex-col">
							<span>{item.label}</span>
							{item.hint && <span className="text-muted-foreground text-xs">{item.hint}</span>}
						</span>
					</label>
				))}
			</div>
		</fieldset>
	);
}