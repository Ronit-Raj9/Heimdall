import { Lock } from "lucide-react";
import { useTranslation } from "react-i18next";

/** The note a built-in library suite shows where editing would be: it is read-only for every org. */
export function BuiltinReadOnlyNote({ testId = "suite-builtin-readonly" }: { testId?: string }) {
	const { t } = useTranslation("suitesUx");
	return (
		<p role="note" className="text-muted-foreground flex items-start gap-2 rounded-xl border px-4 py-2.5 text-sm" data-testid={testId}>
			<Lock className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
			<span>
				<span className="text-foreground font-medium">{t("builtinReadOnly")}</span>. {t("builtinReadOnlyHint")}
			</span>
		</p>
	);
}
