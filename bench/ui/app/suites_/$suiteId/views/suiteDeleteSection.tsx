import "@/components/agents/hw02I18n";
import "@/components/suites/w4iwidI18n";
import { ConfirmButton } from "@/components/platform/badges";
import { DELETE_ROLE } from "@/lib/constants/deleteRole";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { authoringError, useDeleteSuiteMutation } from "@/lib/store/apis/suiteAuthoringApi";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

/**
 * Settings tab, "Delete this benchmark" (fix wave 3, N288): members delete an organization benchmark
 * after one inline question; its comparisons, evaluations and runs stay readable. Built-in library
 * benchmarks and viewers see nothing. A refusal (running work, a schedule) shows its plain reason.
 */
export function SuiteDeleteSection({ suiteId, builtin }: { suiteId: string; builtin: boolean }) {
	const { t } = useTranslation("hw02");
	// M6-35: the same question and labels as the Delete of a benchmarks list row (w4iwid delete.*).
	const { t: tw } = useTranslation("w4iwid");
	const session = useSession();
	const navigate = useNavigate();
	const [remove, state] = useDeleteSuiteMutation();
	if (builtin || !canUse(session, DELETE_ROLE)) return null;
	return (
		// M6-35: a plain section under a top border, not a red card; the button is an outline, not a filled red one.
		<section className="mt-2 flex min-w-0 flex-col gap-2 border-t pt-5" data-testid="suite-delete">
			<h3 className="text-sm font-semibold">{t("suiteDelete.title")}</h3>
			<p className="text-muted-foreground text-sm">{t("suiteDelete.body")}</p>
			<div>
				<ConfirmButton
					label={t("suiteDelete.button")}
					question={tw("delete.suite", { name: suiteId })}
					confirmLabel={tw("delete.confirm")}
					cancelLabel={tw("delete.cancel")}
					variant="outline"
					className="text-destructive border-destructive/40 hover:text-destructive"
					isLoading={state.isLoading}
					testId="suite-delete-button"
					onConfirm={() =>
						void remove(suiteId)
							.unwrap()
							.then(() => navigate({ to: "/suites" }))
							.catch(() => undefined)
					}
				/>
			</div>
			{state.error !== undefined && (
				<p role="alert" className="text-destructive text-sm" data-testid="suite-delete-error">
					{authoringError(state.error, t("suiteDelete.failed"))}
				</p>
			)}
		</section>
	);
}