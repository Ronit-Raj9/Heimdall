import { Link } from "@tanstack/react-router";
import { Lock } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * The note a built-in library suite shows where editing would be: it is read-only for every org.
 * With makeOwn (the benchmark page) it also says how to get an editable benchmark and links to
 * Add a benchmark (one sentence, Q-55); the Build, Data, Policy and Review tabs stay hidden for built-in suites.
 */
export function BuiltinReadOnlyNote({ testId = "suite-builtin-readonly", makeOwn = false }: { testId?: string; makeOwn?: boolean }) {
	const { t } = useTranslation("suitesUx");
	return (
		<p role="note" className="text-muted-foreground flex w-fit max-w-full items-start gap-2 rounded-xl border px-3 py-2 text-sm" data-testid={testId}>
			<Lock className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
			<span>
				<span className="text-foreground font-medium">{t("builtinReadOnly")}</span>. {t("builtinReadOnlyHint")}
				{makeOwn && (
					<>
						{" "}
						<Link to="/suites/add" className="text-foreground font-medium underline underline-offset-2" data-testid={`${testId}-add`}>
							{t("builtinMakeOwnLink")}
						</Link>
					</>
				)}
			</span>
		</p>
	);
}