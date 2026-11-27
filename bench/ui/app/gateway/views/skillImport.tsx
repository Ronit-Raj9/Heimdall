import { Button } from "@/components/ui/button";
import { useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import {
	useGetGatewaySkillVersionsQuery,
	useGetGatewaySkillsQuery,
	useImportSkillFromBifrostMutation,
	type GwSkillImport,
} from "@/lib/store/apis/gatewaySkillsApi";
import { errorMessage } from "@/lib/store/apis/schedulesApi";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import "./uf7cI18n";
import "./w4intI18n";

/** What the import answered, in the page's words: the scan's outcome, or the server's refusal sentence as it is. Pure. */
export function importOutcome(
	result: { data?: GwSkillImport; error?: unknown },
	t: (key: "skillImport.installed" | "skillImport.blocked" | "skillImport.failed", o?: { folder: string }) => string,
): { text: string; ok: boolean; blocked?: boolean } {
	if (result.data) return { text: t("skillImport.installed", { folder: result.data.folder }), ok: true };
	const body = (result.error as { data?: unknown } | undefined)?.data as Partial<GwSkillImport> | undefined;
	if (body && typeof body.folder === "string" && body.scan) return { text: t("skillImport.blocked", { folder: body.folder }), ok: false, blocked: true };
	return { text: errorMessage(result.error, t("skillImport.failed")), ok: false };
}

/** The import form: agent, skill and version, then Import. Its hooks run only once it is opened. */
function SkillImportForm({ onClose }: { onClose(): void }) {
	const { t } = useTranslation("w4int");
	const { t: u } = useTranslation("uf7c");
	const agents = useGetAgentsQuery();
	const skills = useGetGatewaySkillsQuery();
	const [agent, setAgent] = useState("");
	const [skill, setSkill] = useState("");
	const [version, setVersion] = useState("");
	const versions = useGetGatewaySkillVersionsQuery(skill, { skip: !skill });
	const [run, running] = useImportSkillFromBifrostMutation();
	const [outcome, setOutcome] = useState<{ text: string; ok: boolean; blocked?: boolean } | null>(null);
	const onImport = async () => {
		setOutcome(null);
		setOutcome(importOutcome(await run({ agent, skill_id: skill, version }), (k, o) => String(t(k, o as never))));
	};
	const select = "border-input bg-background h-9 rounded-md border px-2 text-sm";
	return (
		<div className="flex flex-col gap-2 rounded-lg border p-3 text-sm" data-testid="gateway-skill-import">
			{skills.isLoading && <p className="text-muted-foreground">{t("skillImport.loading")}</p>}
			{skills.isError && <p className="text-destructive">{errorMessage(skills.error, t("skillImport.failed"))}</p>}
			{skills.data && skills.data.skills.length === 0 && <p className="text-muted-foreground">{t("skillImport.none")}</p>}
			<div className="flex flex-wrap items-end gap-2">
				<label className="flex flex-col gap-1">
					{t("skillImport.agent")}
					<select className={select} value={agent} onChange={(e) => setAgent(e.target.value)} data-testid="gateway-skill-import-agent">
						<option value="">{t("skillImport.pick")}</option>
						{(agents.data?.agents ?? []).map((a) => (
							<option key={a.id} value={a.id}>
								{a.id}
							</option>
						))}
					</select>
				</label>
				<label className="flex flex-col gap-1">
					{t("skillImport.skill")}
					<select
						className={select}
						value={skill}
						onChange={(e) => {
							setSkill(e.target.value);
							setVersion("");
						}}
						data-testid="gateway-skill-import-skill"
					>
						<option value="">{t("skillImport.pick")}</option>
						{(skills.data?.skills ?? []).map((s) => (
							<option key={s.id} value={s.id}>
								{s.name}
							</option>
						))}
					</select>
				</label>
				<label className="flex flex-col gap-1">
					{t("skillImport.version")}
					<select
						className={select}
						value={version}
						onChange={(e) => setVersion(e.target.value)}
						disabled={!skill}
						data-testid="gateway-skill-import-version"
					>
						<option value="">{t("skillImport.pick")}</option>
						{(versions.data?.versions ?? []).map((v) => (
							<option key={v.version} value={v.version}>
								{v.version}
							</option>
						))}
					</select>
				</label>
				<Button
					size="sm"
					disabled={!agent || !skill || !version || running.isLoading}
					onClick={() => void onImport()}
					data-testid="gateway-skill-import-run"
				>
					{t("skillImport.import")}
				</Button>
				<Button size="sm" variant="ghost" onClick={onClose}>
					{t("skillImport.cancel")}
				</Button>
			</div>
			{outcome && (
				<p
					className={outcome.ok ? "" : "text-destructive"}
					role={outcome.ok ? "status" : "alert"}
					data-testid="gateway-skill-import-outcome"
				>
					{outcome.text}
					{outcome.blocked && agent && (
						<>
							{" "}
							<Link to="/agents/$agentId" params={{ agentId: agent }} search={{ tab: "tools" } as never} className="text-primary hover:underline">
								{u("skill.openTools")}
							</Link>
						</>
					)}
				</p>
			)}
		</div>
	);
}

/** On the Gateway page's skill version line: "Import a skill from Bifrost", opening the form. */
export function SkillImportAction() {
	const { t } = useTranslation("w4int");
	const [open, setOpen] = useState(false);
	if (open) return <SkillImportForm onClose={() => setOpen(false)} />;
	return (
		<div>
			<Button type="button" size="sm" variant="outline" onClick={() => setOpen(true)} data-testid="gateway-skill-import-open">
				{t("skillImport.open")}
			</Button>
		</div>
	);
}