import { ModelPicker, type ModelRef } from "@/components/models/modelPicker";
import { Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { useCreateAgentFromYamlMutation, useGetGatewayAgentsQuery, useImportGatewayAgentMutation } from "@/lib/store/apis/gatewayApi";
import { errorMessage } from "@/lib/store/apis/schedulesApi";
import type { GwRegisteredAgent } from "@/lib/types/gateway";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import "./uf7cI18n";
import "./w4p6I18n";

/** Import a registration, then save its agent file. Answers the saved agent's id. */
export async function benchmarkRegisteredAgent(
	calls: {
		importAgent(arg: { name: string; model: string; provider: string; id?: string }): Promise<{ agent_id: string; yaml: string }>;
		createAgent(arg: { yaml: string }): Promise<{ id: string }>;
	},
	arg: { name: string; model: string; provider: string; id?: string },
): Promise<string> {
	const imported = await calls.importAgent(arg);
	const saved = await calls.createAgent({ yaml: imported.yaml });
	return saved.id || imported.agent_id;
}

/** The id the new agent gets: the registration's name in the server's agent id rule (lower case, digits, dots, hyphens, underscores). Pure. */
export function agentIdFor(name: string): string {
	return name
		.toLowerCase()
		.replace(/[^a-z0-9._-]+/g, "-")
		.replace(/^[^a-z0-9]+/, "")
		.slice(0, 64);
}

/** The list of registrations; a switched off one cannot be benchmarked. Pure. */
export function RegisteredAgentsList({ agents, onPick }: { agents: GwRegisteredAgent[]; onPick(name: string): void }) {
	const { t } = useTranslation("w4p6");
	const { t: u } = useTranslation("uf7c");
	if (agents.length === 0) return <p className="text-muted-foreground text-sm">{t("agents.none")}</p>;
	return (
		<ul className="flex flex-col divide-y rounded-xl border text-sm" data-testid="gateway-agents-list">
			{agents.map((a) => (
				<li key={a.name} className="flex flex-wrap items-center gap-2 p-3" data-testid={`gateway-agent-${a.name}`}>
					<span className="font-medium">{a.name}</span>
					{!a.enabled && <span className="text-muted-foreground text-xs">{t("agents.disabled")}</span>}
					<Button type="button" size="sm" variant="outline" className="ml-auto" disabled={!a.enabled} onClick={() => onPick(a.name)}>
						{u("agents.create")}
					</Button>
				</li>
			))}
		</ul>
	);
}

/** Gateway page: "Agents registered in your Bifrost". Asks to connect first when no Bifrost is connected; the list is not fetched then. */
export function RegisteredAgents({ connected }: { connected: boolean }) {
	const { t } = useTranslation("w4p6");
	const { t: u } = useTranslation("uf7c");
	const navigate = useNavigate();
	const q = useGetGatewayAgentsQuery(undefined, { skip: !connected });
	const [importAgent] = useImportGatewayAgentMutation();
	const [createAgent] = useCreateAgentFromYamlMutation();
	const [picked, setPicked] = useState("");
	const [model, setModel] = useState<ModelRef | null>(null);
	const [error, setError] = useState("");
	const [busy, setBusy] = useState(false);
	const go = async () => {
		setBusy(true);
		setError("");
		try {
			const id = await benchmarkRegisteredAgent(
				{ importAgent: (a) => importAgent(a).unwrap(), createAgent: (a) => createAgent(a).unwrap() },
				{ name: picked, model: model?.model ?? "", provider: model?.provider ?? "", id: agentIdFor(picked) || undefined },
			);
			void navigate({ to: "/agents/$agentId", params: { agentId: id } });
		} catch (e) {
			setError(t("agents.error", { error: errorMessage(e, String(e)) }));
		} finally {
			setBusy(false);
		}
	};
	// A 409 means no Bifrost is connected: the same sentence as when the page already knows it.
	const notConnected = !connected || (q.error as { status?: number } | undefined)?.status === 409;
	return (
		<Section title={t("agents.title")} testId="gateway-agents">
			{notConnected ? (
				<p className="text-muted-foreground text-sm" data-testid="gateway-agents-connect">
					{u("agents.connect")}
				</p>
			) : q.error ? (
				<p className="text-destructive text-sm" role="alert">
					{t("agents.error", { error: errorMessage(q.error, String(q.error)) })}
				</p>
			) : q.data ? (
				<RegisteredAgentsList agents={q.data.agents} onPick={setPicked} />
			) : null}
			{picked && (
				<div className="flex flex-col gap-2 text-sm" data-testid="gateway-agent-import">
					<p>{u("agents.willBe", { name: agentIdFor(picked) || picked })}</p>
					<div className="flex flex-col gap-1">
						<span>{t("agents.model")}</span>
						<ModelPicker value={model} onChange={setModel} purpose="agent" testId="gateway-agent-model" />
					</div>
					<div className="flex gap-2">
						<Button type="button" size="sm" disabled={busy || !model?.model || !model.provider} onClick={() => void go()}>
							{u("agents.go")}
						</Button>
						<Button type="button" size="sm" variant="ghost" onClick={() => setPicked("")}>
							{t("agents.cancel")}
						</Button>
					</div>
				</div>
			)}
			{error && (
				<p className="text-destructive text-sm" role="alert">
					{error}
				</p>
			)}
		</Section>
	);
}
