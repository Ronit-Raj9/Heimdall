import { Button } from "@/components/ui/button";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useGetAgentSkillsQuery, useScanAgentSkillMutation } from "@/lib/store/apis/agentopsApi";
import type { AgentSkill } from "@/lib/types/agentops";
import { ShieldAlert } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import "./hw02I18n";

/** Skills of the agent that keep its runs refused: not scanned, or blocked by their scan. */
export function unscannedSkills(skills: readonly AgentSkill[] | undefined): AgentSkill[] {
	return (skills ?? []).filter((s) => s.status === "not_scanned" || s.status === "blocked");
}

interface ScanNote {
	name: string;
	text: string;
}

/**
 * The agent page's warning (fix wave 3, L5): while any uploaded skill has not passed a scan, every run
 * of the agent is refused, so the page says so and offers "Scan now" (each not-scanned skill, one
 * after another, with each verdict) and a way to the Tools tab. Nothing shows when every skill passed.
 */
export function SkillScanBar({ agentId, onOpenTools }: { agentId: string; onOpenTools: () => void }) {
	const { t } = useTranslation("hw02");
	const skills = useGetAgentSkillsQuery(agentId);
	const [scan] = useScanAgentSkillMutation();
	const [busy, setBusy] = useState<string | null>(null);
	const [notes, setNotes] = useState<ScanNote[]>([]);
	const pending = unscannedSkills(skills.data?.skills);
	if (pending.length === 0 && notes.length === 0) return null;
	const toScan = pending.filter((s) => s.status === "not_scanned");

	const scanAll = async () => {
		const out: ScanNote[] = [];
		for (const s of toScan) {
			setBusy(s.name);
			try {
				const res = await scan({ id: agentId, name: s.name }).unwrap();
				out.push({ name: s.name, text: t(res.installed ? "scanBar.admitted" : "scanBar.blocked", { name: s.name }) });
			} catch (e) {
				// The server's message in plain words, never a bare status code (M2-69).
				const msg = getBenchErrorMessage(e);
				out.push({ name: s.name, text: t("scanBar.failed", { name: s.name, error: msg }) });
			}
			setNotes([...out]);
		}
		setBusy(null);
	};

	return (
		<div
			role="status"
			className="flex min-w-0 flex-col gap-2 rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm"
			data-testid="skill-scan-bar"
		>
			{pending.length > 0 && (
				<div className="flex min-w-0 flex-wrap items-center gap-3">
					<ShieldAlert className="size-4 shrink-0 text-amber-600 dark:text-amber-400" aria-hidden />
					<span className="min-w-0 flex-1" data-testid="skill-scan-bar-text">
						{t("scanBar.notPassed", { count: pending.length })}
					</span>
					{toScan.length > 0 && (
						<Button
							type="button"
							size="sm"
							variant="outline"
							isLoading={busy !== null}
							disabled={busy !== null}
							onClick={() => void scanAll()}
							dataTestId="skill-scan-bar-scan"
						>
							{busy ? t("scanBar.scanning", { name: busy }) : t("scanBar.scanNow")}
						</Button>
					)}
					<Button type="button" size="sm" variant="ghost" onClick={onOpenTools} dataTestId="skill-scan-bar-tools">
						{t("scanBar.openTools")}
					</Button>
				</div>
			)}
			{notes.length > 0 && (
				<ul className="text-muted-foreground flex flex-col gap-1 text-xs" data-testid="skill-scan-bar-results">
					{notes.map((n) => (
						<li key={n.name}>{n.text}</li>
					))}
				</ul>
			)}
		</div>
	);
}