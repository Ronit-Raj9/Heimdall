import { Button } from "@/components/ui/button";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useDeleteAgentMutation } from "@/lib/store/apis/agentDeleteApi";
import { useEffect, useId } from "react";
import { useTranslation } from "react-i18next";
import "./fw13I18n";

export interface DeleteAgentDialogProps {
	agentId: string;
	/** Called after the server removed the agent (the caller leaves the agent page). */
	onDeleted(): void;
	onClose(): void;
}

/**
 * Confirms deleting an agent. Only its settings file goes: past runs, results and saved versions
 * stay readable. A refusal from the server (built-in agent, evaluation still running) is shown as is.
 */
export function DeleteAgentDialog({ agentId, onDeleted, onClose }: DeleteAgentDialogProps) {
	const { t } = useTranslation("fw13");
	const [deleteAgent, { isLoading, error }] = useDeleteAgentMutation();
	const titleId = useId();
	useEffect(() => {
		const onKey = (e: KeyboardEvent) => e.key === "Escape" && !isLoading && onClose();
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [onClose, isLoading]);
	const confirm = async () => {
		try {
			await deleteAgent(agentId).unwrap();
			onDeleted();
		} catch {
			// The error is shown from the mutation state.
		}
	};
	return (
		<div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" data-testid="delete-agent-backdrop">
			<div
				role="alertdialog"
				aria-modal="true"
				aria-labelledby={titleId}
				className="bg-card text-card-foreground w-full max-w-md space-y-4 rounded-lg border p-5 text-left shadow-lg"
				data-testid="delete-agent-dialog"
			>
				<div className="space-y-1">
					<h2 id={titleId} className="text-lg font-semibold">
						{t("deleteAgent.title", { id: agentId })}
					</h2>
					<p className="text-muted-foreground text-sm">{t("deleteAgent.body")}</p>
				</div>
				{error !== undefined && (
					<p role="alert" className="text-destructive text-sm" data-testid="delete-agent-error">
						{t("deleteAgent.failed", { message: getBenchErrorMessage(error) })}
					</p>
				)}
				<div className="flex justify-end gap-2">
					<Button type="button" variant="outline" onClick={onClose} disabled={isLoading} dataTestId="delete-agent-keep">
						{t("deleteAgent.keep")}
					</Button>
					<Button type="button" variant="destructive" onClick={() => void confirm()} disabled={isLoading} dataTestId="delete-agent-confirm">
						{isLoading ? t("deleteAgent.deleting") : t("deleteAgent.confirm")}
					</Button>
				</div>
			</div>
		</div>
	);
}