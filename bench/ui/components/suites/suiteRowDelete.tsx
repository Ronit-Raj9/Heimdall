import { useConfirm, type ConfirmFn } from "@/components/platform/confirmDialog";
import { Button } from "@/components/ui/button";
import { DELETE_ROLE } from "@/lib/constants/deleteRole";
import { canUse, useSession, type SessionInfo } from "@/lib/session/sessionContext";
import { authoringError, useDeleteSuiteMutation } from "@/lib/store/apis/suiteAuthoringApi";
import type { SuiteSummary } from "@/lib/types/bench";
import { Trash2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { isBuiltinSuite } from "./suiteDisplay";
import "./w4iwidI18n";
import i18n from "@/lib/i18n";
import "@/components/platform/ufintI18n";

/** Built-in ids a hosted list may hide but that stay built-in names (the server's practice suites, N46). */
const RESERVED_SUITE_IDS: ReadonlySet<string> = new Set(["coding-core"]);

/** Whether the benchmarks list offers Delete on this row: the role the server lets delete (DELETE_ROLE), never on a built-in benchmark (N288). */
export function suiteRowDeletable(session: SessionInfo, suite: Pick<SuiteSummary, "id" | "builtin" | "source">): boolean {
	return canUse(session, DELETE_ROLE) && !isBuiltinSuite(suite) && !RESERVED_SUITE_IDS.has(suite.id);
}

/** Asks once, then deletes. Resolves false when the person cancelled; a refusal stays on the mutation state. */
export async function deleteSuiteRow(
	confirm: ConfirmFn,
	remove: (id: string) => Promise<unknown>,
	id: string,
	question: string,
	labels: { title: string; confirm: string; cancel: string },
): Promise<boolean> {
	if (!(await confirm(question, { title: labels.title, confirmLabel: labels.confirm, cancelLabel: labels.cancel, destructive: true }))) return false;
	await remove(id).catch(() => undefined);
	return true;
}

/**
 * The Delete action of one benchmarks list row (N288, X4): one in-page question, then DELETE /suites/{id}. The server
 * refuses a benchmark a schedule or a comparison still uses; its plain sentence shows under the button.
 */
export function SuiteRowDelete({ suite, name }: { suite: Pick<SuiteSummary, "id" | "builtin" | "source">; name: string }) {
	const { t } = useTranslation("w4iwid");
	const session = useSession();
	const confirm = useConfirm();
	const [remove, state] = useDeleteSuiteMutation();
	if (!suiteRowDeletable(session, suite)) return null;
	const testId = `suites-row-${suite.id}-delete`;
	return (
		<div className="relative z-10 flex flex-col items-end gap-1">
			<Button
				size="sm"
				variant="ghost"
				isLoading={state.isLoading}
				dataTestId={testId}
				onClick={() =>
					void deleteSuiteRow(confirm, (id) => remove(id).unwrap(), suite.id, t("delete.suite", { name }), {
						title: i18n.t("confirm.deleteSuite", { ns: "ufint", name }),
						confirm: t("delete.confirm"),
						cancel: t("delete.cancel"),
					})
				}
			>
				<Trash2 aria-hidden="true" />
				{t("delete.label")}
			</Button>
			{state.error !== undefined && (
				<p role="alert" className="text-destructive max-w-xs text-right text-xs" data-testid={`${testId}-error`}>
					{authoringError(state.error, t("delete.suiteFailed"))}
				</p>
			)}
		</div>
	);
}