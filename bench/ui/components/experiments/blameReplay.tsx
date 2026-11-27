import { JobRunButton } from "@/components/jobs/jobRunButton";
import { useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import type { BlameReplayJobParams, BlameReplayResult } from "@/lib/types/refCompare";
import { Link } from "@tanstack/react-router";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { useLiveJob } from "./liveJob";

const TERMINAL = new Set(["succeeded", "failed", "cancelled"]);

/**
 * Tier-2 blame replay (gap lane G07): the blame.replay job re-runs each blamed team failure with
 * the suspect swapped for a stub agent, through the launcher, and writes the result into
 * blame.json. Shown on the Analysis tab's blame card.
 */
export function BlameReplay({ expId }: { expId: string }) {
	const { t } = useTranslation("refCompare");
	const uid = useId();
	const agents = useGetAgentsQuery();
	const [stub, setStub] = useState("");
	const [jobId, setJobId] = useState<string | null>(null);
	const job = useLiveJob(jobId, 3000);
	const done = job.data?.status ? TERMINAL.has(job.data.status) : false;
	const result = done && job.data?.status === "succeeded" ? (job.data.result as BlameReplayResult | undefined) : undefined;
	const params: BlameReplayJobParams = stub ? { exp_id: expId, stub } : { exp_id: expId };
	return (
		<div className="flex flex-col gap-2 border-t pt-2" data-testid="blame-replay">
			<span className="font-medium">{t("replay.title")}</span>
			<span className="text-muted-foreground text-xs">{t("replay.description")}</span>
			<label className="flex flex-col gap-1 text-xs" htmlFor={`${uid}-stub`}>
				{t("replay.stub")}
				<select
					id={`${uid}-stub`}
					className="border-input bg-background h-8 rounded-md border px-2 text-xs"
					value={stub}
					onChange={(e) => setStub(e.target.value)}
					data-testid="blame-replay-stub"
				>
					<option value="">{t("replay.stubDefault")}</option>
					{(agents.data?.agents ?? []).map((a) => (
						<option key={a.path} value={a.path}>
							{a.id}
						</option>
					))}
				</select>
			</label>
			<div>
				<JobRunButton kind="blame.replay" params={params} label={t("replay.run")} confirm={t("replay.confirm", { expId })} variant="outline" onDone={(j) => setJobId(j.id)} />
			</div>
			{job.data && !done && <span className="text-muted-foreground text-xs">{job.data.status}</span>}
			{job.data?.status === "failed" && (
				<span className="text-destructive text-xs" role="alert" data-testid="blame-replay-error">
					{job.data.error}
				</span>
			)}
			{result && (
				<div className="flex flex-col gap-1 text-xs" data-testid="blame-replay-result">
					<span>
						{result.candidates === 0
							? t("replay.none")
							: t("replay.result", {
									replayed: result.replays.length,
									candidates: result.candidates,
								})}
					</span>
					{result.replays.map((r) => (
						<span key={r.run_id} className="font-mono">
							{r.run_id}: {r.status}, {r.tier}
							{r.replay_exp_id && (
								<>
									{" "}
									<Link to="/experiments/$expId" params={{ expId: r.replay_exp_id }} className="underline">
										{r.replay_exp_id}
									</Link>
								</>
							)}
						</span>
					))}
				</div>
			)}
		</div>
	);
}
