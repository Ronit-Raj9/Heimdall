import { cn } from "@/lib/utils";
import "@/components/runs/screenStatesI18n";
import { cloneElement, isValidElement, useId, type ReactElement, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

export interface FieldProps {
	label: ReactNode;
	/** Required fields carry no mark (the rule: mark the optional ones). Sets aria-required on the control. */
	required?: boolean;
	/** Adds "(optional)" after the label. Never use asterisks. */
	optional?: boolean;
	/** One line under the control. */
	hint?: ReactNode;
	/** The field's own error, under the control, linked by aria-describedby. */
	error?: ReactNode;
	/** One control; it gets id, aria-describedby, aria-invalid and aria-required. */
	children: ReactElement<Record<string, unknown>>;
	className?: string;
	testId?: string;
}

/** The ids a field links its control to: the hint and error lines it describes itself with. */
export function describedBy(id: string, hint: boolean, error: boolean): string | undefined {
	const ids = [hint && `${id}-hint`, error && `${id}-error`].filter(Boolean);
	return ids.length ? ids.join(" ") : undefined;
}

/**
 * A labelled form field (M1-49): label, "(optional)" when optional, the control, a hint and the
 * error. The rule: forms mark optional fields "(optional)" and never use asterisks.
 */
export function Field({ label, required, optional, hint, error, children, className, testId }: FieldProps) {
	const { t } = useTranslation("ufs2");
	const auto = useId();
	const id = (isValidElement(children) && typeof children.props.id === "string" ? children.props.id : auto) as string;
	const control = isValidElement(children)
		? cloneElement(children, {
				id,
				"aria-describedby": describedBy(id, !!hint, !!error),
				"aria-invalid": error ? true : undefined,
				"aria-required": required || undefined,
			})
		: children;
	return (
		<div className={cn("flex flex-col gap-1.5", className)} data-testid={testId}>
			<label htmlFor={id} className="text-foreground text-sm font-medium">
				{label}
				{optional && <span className="text-muted-foreground ml-1 font-normal">{t("field.optional")}</span>}
			</label>
			{control}
			{hint && (
				<p id={`${id}-hint`} className="text-muted-foreground text-xs">
					{hint}
				</p>
			)}
			{error && (
				<p id={`${id}-error`} className="text-chart-error-ink text-xs" role="alert">
					{error}
				</p>
			)}
		</div>
	);
}
