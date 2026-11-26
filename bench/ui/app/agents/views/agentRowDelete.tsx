import { useConfirm, type ConfirmFn } from "@/components/platform/confirmDialog";
import "@/components/suites/w4iwidI18n";
import { Button } from "@/components/ui/button";
import { canUse, useSession, type SessionInfo } from "@/lib/session/sessionContext";
import { agentBuilderApi } from "@/lib/store/apis/agentBuilderApi";
import { useDeleteAgentMutation } from "@/lib/store/apis/agentDeleteApi";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import type { AgentSummary } from "@/lib/types/bench";
import { Trash2 } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { isBaselineAgent } from "./agentDisplay";

/** Whether the agents list offers Delete on this card: admins only, never on a built-in baseline agent (X4). */
export function agentRowDeletable(session: SessionInfo, agent: Pick<AgentSummary, "id">): boolean {
	return canUse(session, "admin") && agent.id !== "" && !isBaselineAgent(agent);
}

export type AgentRowDeleteOutcome = "builtin" | "cancelled" | "asked";

/**
 * Checks the agent has a settings file in this project (a built-in library agent has none: GET /agents/{id}/config
 * answers 404), then asks once and deletes. A built-in agent is never asked about; a refusal stays on the mutation state.
 */
export async function deleteAgentRow(
	hasOwnFile: (id: string) => Promise<boolean>,
	confirm: ConfirmFn,
	remove: (id: string) => Promise<unknown>,
	id: string,
	question: string,
	labels: { confirm: string; cancel: string },
): Promise<AgentRowDeleteOutcome> {
	if (!(await hasOwnFile(id))) return "builtin";
	if (!(await confirm(question, { confirmLabel: labels.confirm, cancelLabel: labels.cancel, destructive: true }))) return "cancelled";
	await remove(id).catch(() => undefined);
	return "asked";
}

/** The Delete action of one agents list card (N288, X4): only the settings file goes, past runs and results stay. */
export function AgentRowDelete({ agent, name, testId }: { agent: Pick<AgentSummary, "id">; name: string; testId: string }) {
	const { t } = useTranslation("w4iwid");
	const session = useSession();
	const confirm = useConfirm();
	const [remove, state] = useDeleteAgentMutation();
	const [readConfig] = agentBuilderApi.endpoints.getAgentConfig.useLazyQuery();
	const [builtin, setBuiltin] = useState(false);
	if (!agentRowDeletable(session, agent)) return null;
	const hasOwnFile = (id: string) =>
		readConfig(id, true)
			.unwrap()
			.then(() => true)
			.catch((e: unknown) => (e as { status?: unknown } | undefined)?.status !== 404);
	const run = async () => {
		const outcome = await deleteAgentRow(hasOwnFile, confirm, (id) => remove(id).unwrap(), agent.id, t("delete.agent", { name }), {
			confirm: t("delete.confirm"),
			cancel: t("delete.cancel"),
		});
		setBuiltin(outcome === "builtin");
	};
	const refusal = builtin
		? t("delete.builtinAgent")
		: state.error !== undefined
			? getBenchErrorMessage(state.error) || t("delete.agentFailed")
			: null;
	return (
		<div className="relative z-10 flex flex-col items-end gap-1">
			<Button size="sm" variant="ghost" isLoading={state.isLoading} dataTestId={`${testId}-delete`} onClick={() => void run()}>
				<Trash2 aria-hidden="true" />
				{t("delete.label")}
			</Button>
			{refusal && (
				<p role="alert" className="text-destructive max-w-xs text-right text-xs" data-testid={`${testId}-delete-error`}>
					{refusal}
				</p>
			)}
		</div>
	);
}