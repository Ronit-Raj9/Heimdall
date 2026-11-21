import { JobRunButton } from "@/components/jobs/jobRunButton";
import { EmptyState } from "@/components/shell/emptyState";
import { Input } from "@/components/ui/input";
import { useSession } from "@/lib/session/sessionContext";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { type AgentOpsTabProps, Field, OpsSection, useAgentId, useAgentSummary } from "./agentOps.shared";

const DEFAULT_SUITE = "coding-core";

/**
 * The Checks tab of an agent: a free conformance check (agent.check), an image build
 * (agent.build, local only) and a version diff (agent.diff, local; hosted opens the Diff page).
 * Each button is a background job with a live log.
 */
export function ChecksTab(props: AgentOpsTabProps = {}) {
	const id = useAgentId(props);
	const { agent, isLoading } = useAgentSummary(id);
	const session = useSession();
	const hosted = session.authMode === "hosted";
	const [suite, setSuite] = useState(DEFAULT_SUITE);
	const [task, setTask] = useState("");
	const [noBuild, setNoBuild] = useState(false);
	const [buildSuite, setBuildSuite] = useState("");

	if (isLoading) return <p className="text-muted-foreground p-4 text-sm">Loading agent...</p>;
	if (!agent) {
		return (
			<EmptyState
				compact
				message={`No agent "${id}" in this project. Create it first.`}
				action={{ label: "New agent", to: "/agents_/new" }}
				testId="checks-no-agent"
			/>
		);
	}
	const checkParams = { agent: agent.path, suite: suite.trim(), ...(task.trim() ? { task: task.trim() } : {}), ...(noBuild ? { no_build: true } : {}) };
	const buildParams = { agent: agent.path, ...(buildSuite.trim() ? { suite: buildSuite.trim() } : {}) };

	return (
		<div className="space-y-4" data-testid="agent-checks-tab">
			<OpsSection
				title="Conformance check"
				hint="Runs the agent on one task against a scripted model through the fault proxy. Free: no model key is used. Any FAIL row means the agent will not run reliably in experiments."
				testId="agent-check-section"
			>
				<div className="grid gap-3 sm:grid-cols-3">
					<Field label="Suite">
						<Input value={suite} onChange={(e) => setSuite(e.target.value)} data-testid="agent-check-suite" />
					</Field>
					<Field label="Task (optional, default: the suite's first)">
						<Input value={task} onChange={(e) => setTask(e.target.value)} data-testid="agent-check-task" />
					</Field>
					{!hosted && (
						<label className="flex items-center gap-2 self-end pb-2 text-xs">
							<input type="checkbox" checked={noBuild} onChange={(e) => setNoBuild(e.target.checked)} data-testid="agent-check-nobuild" />
							Skip the image build
						</label>
					)}
				</div>
				{suite.trim() === "" ? (
					<p className="text-destructive text-xs">Name a suite to check against, for example {DEFAULT_SUITE}.</p>
				) : (
					<JobRunButton kind="agent.check" params={checkParams} label="Run check" />
				)}
			</OpsSection>

			<OpsSection
				title="Build image"
				hint={
					agent.per_suite_build
						? "This agent builds one image per suite (build.base_arg): name the suite."
						: agent.dockerfile
							? `Builds ${agent.image} from ${agent.dockerfile}.`
							: "This agent has no build.dockerfile: it uses a prebuilt image."
				}
				testId="agent-build-section"
			>
				{hosted ? (
					<p className="text-muted-foreground text-xs">
						Hosted servers do not build images. Push your image to a registry, pin it with @sha256: in the agent config, and use Run check above.
					</p>
				) : !agent.dockerfile ? (
					<p className="text-muted-foreground text-xs">Nothing to build. Add a build section in the Config tab to build here.</p>
				) : (
					<div className="flex flex-wrap items-end gap-3">
						<Field label={agent.per_suite_build ? "Suite (required)" : "Suite (optional)"}>
							<Input value={buildSuite} onChange={(e) => setBuildSuite(e.target.value)} data-testid="agent-build-suite" />
						</Field>
						{agent.per_suite_build && !buildSuite.trim() ? (
							<p className="text-destructive pb-2 text-xs">Name the suite this image is built for.</p>
						) : (
							<JobRunButton kind="agent.build" params={buildParams} label="Build image" variant="outline" />
						)}
					</div>
				)}
			</OpsSection>

			<OpsSection
				title="Compare versions"
				hint="What changed between this agent's two newest config versions and which tasks flipped, over sealed runs."
				testId="agent-diff-section"
			>
				<div className="flex flex-wrap items-center gap-3">
					{!hosted && <JobRunButton kind="agent.diff" params={{ agent: agent.id }} label="Diff newest two versions" variant="outline" />}
					<Link to="/agents" search={{ tab: "diff", agent: agent.id }} className="text-primary text-sm underline" data-testid="agent-diff-link">
						Open the Diff page
					</Link>
				</div>
			</OpsSection>
		</div>
	);
}

export default ChecksTab;
