import { JobRunButton } from "@/components/jobs/jobRunButton";
import { EmptyState } from "@/components/shell/emptyState";
import { Input } from "@/components/ui/input";
import { useSession } from "@/lib/session/sessionContext";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { type AgentOpsTabProps, Field, OpsSection, useAgentId, useAgentSummary } from "./agentOps.shared";

const DEFAULT_SUITE = "coding-core";

/**
 * The Checks tab of an agent: a free conformance check (agent.check), an image build
 * (agent.build; hosted builds run on a runner) and a version diff (agent.diff, local; hosted opens the Diff page).
 * Each button is a background job with a live log.
 */
export function ChecksTab(props: AgentOpsTabProps = {}) {
	const id = useAgentId(props);
	const { agent, isLoading } = useAgentSummary(id);
	const { t } = useTranslation("agentops");
	const session = useSession();
	const hosted = session.authMode === "hosted";
	const [suite, setSuite] = useState(DEFAULT_SUITE);
	const [task, setTask] = useState("");
	const [noBuild, setNoBuild] = useState(false);
	const [buildSuite, setBuildSuite] = useState("");

	if (isLoading) return <p className="text-muted-foreground p-4 text-sm">{t("checks.loading")}</p>;
	if (!agent) {
		return (
			<EmptyState
				compact
				message={t("checks.noAgent", { id })}
				action={{ label: t("checks.newAgent"), to: "/agents/new" }}
				testId="checks-no-agent"
			/>
		);
	}
	const checkParams = { agent: agent.path, suite: suite.trim(), ...(task.trim() ? { task: task.trim() } : {}), ...(noBuild ? { no_build: true } : {}) };
	const buildParams = { agent: agent.path, ...(buildSuite.trim() ? { suite: buildSuite.trim() } : {}) };

	return (
		<div className="space-y-4" data-testid="agent-checks-tab">
			<OpsSection
				title={t("checks.check.title")}
				hint={t("checks.check.hint")}
				testId="agent-check-section"
			>
				<div className="grid gap-3 sm:grid-cols-3">
					<Field label={t("checks.check.suite")}>
						<Input value={suite} onChange={(e) => setSuite(e.target.value)} data-testid="agent-check-suite" />
					</Field>
					<Field label={t("checks.check.task")}>
						<Input value={task} onChange={(e) => setTask(e.target.value)} data-testid="agent-check-task" />
					</Field>
					{!hosted && (
						<label className="flex items-center gap-2 self-end pb-2 text-xs">
							<input type="checkbox" checked={noBuild} onChange={(e) => setNoBuild(e.target.checked)} data-testid="agent-check-nobuild" />
							{t("checks.check.noBuild")}
						</label>
					)}
				</div>
				{suite.trim() === "" ? (
					<p className="text-destructive text-xs">{t("checks.check.needSuite", { suite: DEFAULT_SUITE })}</p>
				) : (
					<JobRunButton kind="agent.check" params={checkParams} label={t("checks.check.run")} />
				)}
			</OpsSection>

			<OpsSection
				title={t("checks.build.title")}
				hint={
					agent.per_suite_build
						? t("checks.build.hintPerSuite")
						: agent.dockerfile
							? t("checks.build.hintDockerfile", { image: agent.image, dockerfile: agent.dockerfile })
							: t("checks.build.hintPrebuilt")
				}
				testId="agent-build-section"
			>
				{hosted && agent.dockerfile && (
					<p className="text-muted-foreground text-xs" data-testid="agent-build-runner-note">
						{t("checks.build.runnerNote")}
					</p>
				)}
				{!agent.dockerfile ? (
					<p className="text-muted-foreground text-xs">{t("checks.build.nothing")}</p>
				) : (
					<div className="flex flex-wrap items-end gap-3">
						<Field label={agent.per_suite_build ? t("checks.build.suiteRequired") : t("checks.build.suiteOptional")}>
							<Input value={buildSuite} onChange={(e) => setBuildSuite(e.target.value)} data-testid="agent-build-suite" />
						</Field>
						{agent.per_suite_build && !buildSuite.trim() ? (
							<p className="text-destructive pb-2 text-xs">{t("checks.build.needSuite")}</p>
						) : (
							<JobRunButton kind="agent.build" params={buildParams} label={t("checks.build.run")} variant="outline" />
						)}
					</div>
				)}
			</OpsSection>

			<OpsSection
				title={t("checks.diff.title")}
				hint={t("checks.diff.hint")}
				testId="agent-diff-section"
			>
				<div className="flex flex-wrap items-center gap-3">
					{!hosted && <JobRunButton kind="agent.diff" params={{ agent: agent.id }} label={t("checks.diff.run")} variant="outline" />}
					<Link to="/agents" search={{ tab: "diff", agent: agent.id }} className="text-primary text-sm underline" data-testid="agent-diff-link">
						{t("checks.diff.open")}
					</Link>
				</div>
			</OpsSection>
		</div>
	);
}

export default ChecksTab;
