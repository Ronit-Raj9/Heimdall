import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import type { RunSkills } from "@/lib/types/bench";
import { Section } from "@/components/runs/screenStates";
import "./ufm2I18n";

/**
 * "Skills in this run": each skill the run installed (from the arm's frozen manifest, handlers/runskills.go)
 * with its file count, and the first step that read it. "Read" is only ever claimed from a step the server found.
 * Nothing renders when the server sent no skills record (older server, or steps unreadable).
 */
export function RunSkillsBlock({ runId, skills }: { runId: string; skills?: RunSkills }) {
	const { t } = useTranslation("ufm2");
	if (!skills) return null;
	const list = skills.installed ?? [];
	return (
		<Section title={t("skills.title")} description={t("skills.help")} testId="run-skills">
			{list.length === 0 ? (
				<p className="text-muted-foreground text-sm" data-testid="run-skills-none">
					{t("skills.none")}
				</p>
			) : (
				<ul className="flex flex-col gap-1.5 text-sm">
					{list.map((s) => (
						<li key={s.name} className="flex flex-wrap items-baseline gap-x-3" data-testid="run-skill" data-read={s.read}>
							<span className="font-mono font-medium">{s.name}</span>
							<span className="text-muted-foreground text-xs">{t("skills.files", { count: s.files })}</span>
							{s.read && s.read_step ? (
								<Link
									to="/runs/$runId"
									params={{ runId }}
									search={{ tab: "timeline", step: s.read_step } as never}
									className="text-xs underline underline-offset-2"
									title={s.read_path}
									data-testid="run-skill-read"
								>
									{t("skills.readAt", { step: s.read_step })}
								</Link>
							) : (
								<span className="text-muted-foreground text-xs" data-testid="run-skill-unread">
									{t("skills.notRead")}
								</span>
							)}
						</li>
					))}
				</ul>
			)}
		</Section>
	);
}