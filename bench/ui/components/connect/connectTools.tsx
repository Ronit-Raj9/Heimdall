import "./ufl6I18n";

import { FailureNote } from "@/app/connect/views/failureNote";
import { InlineError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useGetGithubStatusQuery } from "@/lib/store/apis/githubApi";
import {
	githubAgentFilePath,
	useCheckAgentFileMutation,
	useExplainConnectErrorMutation,
	useReadGithubAgentFileMutation,
} from "@/lib/store/apis/ufl6ConnectApi";
import type { AgentFileCheckResult } from "@/lib/types/ufl6Connect";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";

const areaClass =
	"border-input bg-card focus-visible:ring-ring/40 min-h-20 w-full rounded-md border px-3 py-2 font-mono text-sm outline-none focus-visible:ring-2";

/** Paste a run or check error, get its catalog entry and fix (POST /connect/explain). */
export function ExplainError({ disabled }: { disabled?: boolean }) {
	const { t } = useTranslation("ufl6");
	const id = useId();
	const [text, setText] = useState("");
	const [run, state] = useExplainConnectErrorMutation();
	return (
		<form
			className="flex min-w-0 flex-col gap-2"
			data-testid="connect-explain"
			onSubmit={(e) => {
				e.preventDefault();
				if (text.trim()) void run({ text: text.trim() });
			}}
		>
			<label htmlFor={id} className="text-sm font-medium">
				{t("explain.label")}
			</label>
			<textarea
				id={id}
				className={areaClass}
				placeholder={t("explain.placeholder")}
				value={text}
				onChange={(e) => setText(e.target.value)}
			/>
			<div>
				<Button
					type="submit"
					size="sm"
					variant="outline"
					disabled={disabled || !text.trim()}
					isLoading={state.isLoading}
					data-testid="connect-explain-button"
				>
					{t("explain.button")}
				</Button>
			</div>
			<InlineError error={state.error} testId="connect-explain-error" />
			{state.data && !state.isLoading ? (
				state.data.found && state.data.failure ? (
					<FailureNote failure={state.data.failure} testId="connect-explain-result" />
				) : (
					<p className="text-muted-foreground text-sm" data-testid="connect-explain-none">
						{t("explain.notFound")}
					</p>
				)
			) : null}
		</form>
	);
}

/** Paste a .heimdall/agent.yaml, see whether it is valid and the config it becomes (POST /connect/agent-file/check). */
export function AgentFileCheck({ disabled }: { disabled?: boolean }) {
	const { t } = useTranslation("ufl6");
	const id = useId();
	const [yaml, setYaml] = useState("");
	const [run, state] = useCheckAgentFileMutation();
	const [repo, setRepo] = useState("");
	const [readRepo, repoState] = useReadGithubAgentFileMutation();
	const [last, setLast] = useState<"paste" | "github">("paste");
	const gh = useGetGithubStatusQuery();
	const ghReady = gh.data?.connected === true;
	const repoPath = githubAgentFilePath(repo);
	const busy = state.isLoading || repoState.isLoading;
	const res: AgentFileCheckResult | undefined = busy ? undefined : last === "github" ? repoState.data : state.data;
	const fromGithub = async () => {
		if (!repoPath) return;
		setLast("github");
		const out = await readRepo(repoPath);
		if ("data" in out && out.data?.yaml) setYaml(out.data.yaml);
	};
	return (
		<form
			className="flex min-w-0 flex-col gap-2"
			data-testid="connect-agent-file"
			onSubmit={(e) => {
				e.preventDefault();
				if (!yaml.trim()) return;
				setLast("paste");
				void run({ yaml });
			}}
		>
			<div className="flex min-w-0 flex-wrap items-center gap-2" data-testid="connect-agent-file-github">
				<Input
					value={repo}
					onChange={(e) => setRepo(e.target.value)}
					placeholder="owner/repo"
					aria-label={t("file.repo")}
					disabled={!ghReady}
					className="max-w-xs font-mono"
				/>
				<Button
					type="button"
					size="sm"
					variant="outline"
					disabled={disabled || !ghReady || !repoPath}
					isLoading={repoState.isLoading}
					onClick={() => void fromGithub()}
					data-testid="connect-agent-file-github-read"
				>
					{t("file.fromGithub")}
				</Button>
				{gh.data && !ghReady ? <span className="text-muted-foreground text-xs">{t("file.githubOff")}</span> : null}
			</div>
			{last === "github" ? <InlineError error={repoState.error} testId="connect-agent-file-github-error" /> : null}
			{last === "github" && repoState.data && !repoState.isLoading && !repoState.data.found ? (
				<p className="text-muted-foreground text-sm" data-testid="connect-agent-file-github-none">
					{t("file.githubNone")}
				</p>
			) : null}
			<label htmlFor={id} className="font-mono text-sm font-medium">
				{t("file.label")}
			</label>
			<textarea id={id} className={areaClass} value={yaml} onChange={(e) => setYaml(e.target.value)} spellCheck={false} />
			<div>
				<Button
					type="submit"
					size="sm"
					variant="outline"
					disabled={disabled || !yaml.trim()}
					isLoading={state.isLoading}
					data-testid="connect-agent-file-button"
				>
					{t("file.button")}
				</Button>
			</div>
			{last === "paste" ? <InlineError error={state.error} testId="connect-agent-file-error" /> : null}
			{res && !res.ok && res.error ? (
				<p role="alert" className="text-chart-error-ink text-sm" data-testid="connect-agent-file-problem">
					{res.error}
				</p>
			) : null}
			{res?.ok ? (
				<div className="flex min-w-0 flex-col gap-1" data-testid="connect-agent-file-ok">
					<p className="text-chart-success-ink text-sm">{t("file.ok")}</p>
					{res.config_yaml ? (
						<pre className="bg-muted max-h-64 overflow-auto rounded p-2 font-mono text-xs" aria-label={t("file.config")}>
							{res.config_yaml}
						</pre>
					) : null}
				</div>
			) : null}
		</form>
	);
}