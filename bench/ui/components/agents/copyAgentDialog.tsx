import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import i18n from "@/lib/i18n";
import { AGENT_ID_MAX, agentNameProblem } from "./agentName";
import { useCopyAgentMutation } from "@/lib/store/apis/agentDeleteApi";
import { getBenchErrorMessage, useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import { useEffect, useId, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import "./fw13I18n";

/** Why name cannot be the new name of a copy of agent from, or "" when it can. */
export function copyNameProblem(name: string, from: string, taken: ReadonlySet<string>): string {
	if (name === from) return i18n.t("copyAgent.sameName", { ns: "fw13" });
	return agentNameProblem(name, taken);
}

/** The first of "<id>-copy", "<id>-copy-2", ... that no agent uses. */
export function defaultCopyName(id: string, taken: ReadonlySet<string>): string {
	const base = `${id}-copy`.slice(0, AGENT_ID_MAX);
	if (!taken.has(base)) return base;
	for (let n = 2; ; n++) {
		const name = `${base.slice(0, AGENT_ID_MAX - String(n).length - 1)}-${n}`;
		if (!taken.has(name)) return name;
	}
}

export interface CopyAgentDialogProps {
	agentId: string;
	/** duplicate keeps the original; rename removes it after the copy is written. */
	mode: "duplicate" | "rename";
	/** Called with the new agent's id after the server wrote it (the caller opens it). */
	onCopied(newId: string): void;
	onClose(): void;
}

/**
 * Duplicates or renames an agent (POST /agents/{id}/copy). The copy gets the agent's settings, skills and tools and
 * starts at version 1. A rename also removes the original; its past runs, results and saved versions keep the old
 * name. A refusal from the server (name taken, evaluation still running) is shown as is.
 */
export function CopyAgentDialog({ agentId, mode, onCopied, onClose }: CopyAgentDialogProps) {
	const { t } = useTranslation("fw13");
	const [copyAgent, { isLoading, error }] = useCopyAgentMutation();
	const agents = useGetAgentsQuery();
	const taken = useMemo(() => new Set((agents.data?.agents ?? []).map((a) => a.id)), [agents.data]);
	const [typed, setTyped] = useState<string | null>(null);
	const name = typed ?? (mode === "rename" ? agentId : defaultCopyName(agentId, taken));
	const problem = copyNameProblem(name, agentId, taken);
	const titleId = useId();
	const fieldId = useId();
	const rename = mode === "rename";
	useEffect(() => {
		const onKey = (e: KeyboardEvent) => e.key === "Escape" && !isLoading && onClose();
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [onClose, isLoading]);
	const confirm = async () => {
		if (problem) return;
		try {
			await copyAgent({ id: agentId, new_id: name, remove_original: rename || undefined }).unwrap();
			onCopied(name);
		} catch {
			// The error is shown from the mutation state.
		}
	};
	return (
		<div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" data-testid="copy-agent-backdrop">
			<div
				role="dialog"
				aria-modal="true"
				aria-labelledby={titleId}
				className="bg-card text-card-foreground w-full max-w-md space-y-4 rounded-lg border p-5 text-left shadow-lg"
				data-testid="copy-agent-dialog"
			>
				<div className="space-y-1">
					<h2 id={titleId} className="text-lg font-semibold">
						{rename ? t("copyAgent.titleRename", { id: agentId }) : t("copyAgent.titleDuplicate", { id: agentId })}
					</h2>
					<p className="text-muted-foreground text-sm">
						{rename
							? t("copyAgent.bodyRename", { new: name || t("copyAgent.newNamePlaceholder"), old: agentId })
							: t("copyAgent.bodyDuplicate")}
					</p>
				</div>
				<form
					className="space-y-4"
					onSubmit={(e) => {
						e.preventDefault();
						void confirm();
					}}
				>
					<div className="flex flex-col gap-1 text-sm">
						<label htmlFor={fieldId} className="font-medium">
							{t("copyAgent.newName")}
						</label>
						<Input
							id={fieldId}
							value={name}
							onChange={(e) => setTyped(e.target.value.toLowerCase())}
							aria-invalid={problem !== ""}
							disabled={isLoading}
							autoFocus
							data-testid="copy-agent-name"
						/>
						{problem && (
							<span className="text-destructive text-xs" data-testid="copy-agent-name-problem">
								{problem}
							</span>
						)}
					</div>
					{error !== undefined && (
						<p role="alert" className="text-destructive text-sm" data-testid="copy-agent-error">
							{t("copyAgent.failed", { message: getBenchErrorMessage(error) })}
						</p>
					)}
					<div className="flex justify-end gap-2">
						<Button type="button" variant="outline" onClick={onClose} disabled={isLoading} dataTestId="copy-agent-cancel">
							{t("copyAgent.cancel")}
						</Button>
						<Button type="submit" disabled={isLoading || problem !== ""} dataTestId="copy-agent-confirm">
							{isLoading ? t("copyAgent.saving") : rename ? t("copyAgent.rename") : t("copyAgent.duplicate")}
						</Button>
					</div>
				</form>
			</div>
		</div>
	);
}