import "@/components/agents/hw02I18n";
import { ConfirmButton } from "@/components/platform/badges";
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
	const session = useSession();
	const navigate = useNavigate();
	const [remove, state] = useDeleteSuiteMutation();
	if (builtin || !canUse(session, "member")) return null;
	return (
		<div className="border-destructive/30 mt-5 flex min-w-0 flex-col gap-2 rounded-lg border p-4" data-testid="suite-delete">
			<h3 className="text-sm font-semibold">{t("suiteDelete.title")}</h3>
			<p className="text-muted-foreground text-sm">{t("suiteDelete.body")}</p>
			<div>
				<ConfirmButton
					label={t("suiteDelete.button")}
					question={t("suiteDelete.question", { id: suiteId })}
					confirmLabel={t("suiteDelete.confirm")}
					cancelLabel={t("suiteDelete.cancel")}
					variant="destructive"
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
		</div>
	);
}