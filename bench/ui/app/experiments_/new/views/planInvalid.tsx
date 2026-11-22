import "./builderI18n";

import { Button } from "@/components/ui/button";
import { useTranslation } from "react-i18next";
import type { ExperimentFormValues } from "./experimentFormSchema";

/** The A/B form's fields in the order the page shows them, for the first field to fix (N43). */
export const FORM_FIELD_ORDER = [
	"name",
	"suite",
	"base",
	"armALabel",
	"armBLabel",
	"overridePath",
	"overrideValue",
	"primaryCondition",
	"primaryMetric",
	"conditions",
	"seed",
	"concurrency",
	"runnerPool",
	"totalUsd",
] as const satisfies readonly (keyof ExperimentFormValues)[];

export type FormFieldName = (typeof FORM_FIELD_ORDER)[number];

/** The bench key of each field's label. */
const FIELD_LABEL = {
	name: "experiments.new.fields.name",
	suite: "experiments.new.fields.suite",
	base: "experiments.new.fields.base",
	armALabel: "experiments.new.fields.armA",
	armBLabel: "experiments.new.fields.armB",
	overridePath: "experiments.new.fields.overridePath",
	overrideValue: "experiments.new.fields.overrideValue",
	primaryCondition: "experiments.new.fields.primaryCondition",
	primaryMetric: "experiments.new.fields.primaryMetric",
	conditions: "experiments.new.sections.conditions",
	seed: "experiments.new.fields.seed",
	concurrency: "experiments.new.fields.concurrency",
	runnerPool: "experiments.new.fields.runnerPool",
	totalUsd: "experiments.new.fields.totalUsd",
} as const satisfies Record<FormFieldName, string>;

/** The fields with an error, in form order. errors is react-hook-form's errors object (or any map by field). */
export function invalidFields(errors: Partial<Record<string, unknown>>): FormFieldName[] {
	return FORM_FIELD_ORDER.filter((name) => Boolean(errors[name]));
}

/**
 * Shown next to Plan when the form did not validate: how many fields to fix, and a button per field
 * that brings it into view. Renders nothing when every field is valid.
 */
export function PlanInvalidAlert({ fields, onFocus }: { fields: readonly FormFieldName[]; onFocus: (name: FormFieldName) => void }) {
	const { t } = useTranslation("builder");
	const { t: tb } = useTranslation("bench");
	if (fields.length === 0) return null;
	return (
		<div role="alert" className="text-chart-error-ink flex flex-col gap-1 text-xs" data-testid="plan-invalid">
			<span>{t("actions.fixFields", { count: fields.length })}</span>
			<span className="flex flex-wrap gap-1">
				{fields.map((name) => (
					<Button
						key={name}
						type="button"
						variant="link"
						size="sm"
						className="h-auto p-0 text-xs"
						onClick={() => onFocus(name)}
						dataTestId={`plan-invalid-field-${name}`}
					>
						{tb(FIELD_LABEL[name])}
					</Button>
				))}
			</span>
		</div>
	);
}