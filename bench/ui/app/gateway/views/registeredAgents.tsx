import { Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useCreateAgentFromYamlMutation, useGetGatewayAgentsQuery, useImportGatewayAgentMutation } from "@/lib/store/apis/gatewayApi";
import { errorMessage } from "@/lib/store/apis/schedulesApi";
import type { GwRegisteredAgent } from "@/lib/types/gateway";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import "./w4p6I18n";

/** Import a registration, then save its agent file. Answers the saved agent's id. */
export async function benchmarkRegisteredAgent(
	calls: {
		importAgent(arg: { name: string; model: string; provider: string }): Promise<{ agent_id: string; yaml: string }>;
		createAgent(arg: { yaml: string }): Promise<{ id: string }>;
	},
	arg: { name: string; model: string; provider: string },
): Promise<string> {
	const imported = await calls.importAgent(arg);
	const saved = await calls.createAgent({ yaml: imported.yaml });
	return saved.id || imported.agent_id;
}

/** The list of registrations; a switched off one cannot be benchmarked. Pure. */
export function RegisteredAgentsList({ agents, onPick }: { agents: GwRegisteredAgent[]; onPick(name: string): void }) {
	const { t } = useTranslation("w4p6");
	if (agents.length === 0) return <p className="text-muted-foreground text-sm">{t("agents.none")}</p>;
	return (
		<ul className="flex flex-col divide-y rounded-xl border text-sm" data-testid="gateway-agents-list">
			{agents.map((a) => (
				<li key={a.name} className="flex flex-wrap items-center gap-2 p-3" data-testid={`gateway-agent-${a.name}`}>
					<span className="font-medium">{a.name}</span>
					{!a.enabled && <span className="text-muted-foreground text-xs">{t("agents.disabled")}</span>}
					<Button type="button" size="sm" variant="outline" className="ml-auto" disabled={!a.enabled} onClick={() => onPick(a.name)}>
						{t("agents.benchmark")}
					</Button>
				</li>
			))}
		</ul>
	);
}

/** Gateway page: "Agents registered in your Bifrost". Hidden when the list cannot be read (no source connected). */
export function RegisteredAgents() {
	const { t } = useTranslation("w4p6");
	const navigate = useNavigate();
	const q = useGetGatewayAgentsQuery();
	const [importAgent] = useImportGatewayAgentMutation();
	const [createAgent] = useCreateAgentFromYamlMutation();
	const [picked, setPicked] = useState("");
	const [model, setModel] = useState("");
	const [provider, setProvider] = useState("");
	const [error, setError] = useState("");
	const [busy, setBusy] = useState(false);
	if (!q.data) return null;
	const go = async () => {
		setBusy(true);
		setError("");
		try {
			const id = await benchmarkRegisteredAgent(
				{ importAgent: (a) => importAgent(a).unwrap(), createAgent: (a) => createAgent(a).unwrap() },
				{ name: picked, model: model.trim(), provider: provider.trim() },
			);
			void navigate({ to: "/agents/$agentId", params: { agentId: id } });
		} catch (e) {
			setError(t("agents.error", { error: errorMessage(e, String(e)) }));
		} finally {
			setBusy(false);
		}
	};
	return (
		<Section title={t("agents.title")} testId="gateway-agents">
			<RegisteredAgentsList agents={q.data.agents} onPick={setPicked} />
			{picked && (
				<div className="flex flex-wrap items-end gap-2 text-sm" data-testid="gateway-agent-import">
					<span className="font-medium">{picked}</span>
					<label className="flex flex-col gap-1">
						{t("agents.model")}
						<Input value={model} onChange={(e) => setModel(e.target.value)} />
					</label>
					<label className="flex flex-col gap-1">
						{t("agents.provider")}
						<Input value={provider} onChange={(e) => setProvider(e.target.value)} />
					</label>
					<Button type="button" size="sm" disabled={busy || !model.trim() || !provider.trim()} onClick={() => void go()}>
						{t("agents.go")}
					</Button>
					<Button type="button" size="sm" variant="ghost" onClick={() => setPicked("")}>
						{t("agents.cancel")}
					</Button>
				</div>
			)}
			{error && <p className="text-sm text-red-700 dark:text-red-400">{error}</p>}
		</Section>
	);
}