import "./w449I18n";

import { flipDate } from "@/components/validity/flipBanner";
import { PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { useCreateJobMutation } from "@/lib/store/apis/jobRunApi";
import { type ValidityEntry, type ValidityStale, useGetValidityQuery } from "@/lib/store/apis/validityApi";
import { useSession } from "@/lib/session/sessionContext";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** The rows a reader has not dismissed are bold. */
export function isUnseen(e: ValidityEntry, user: string | undefined): boolean {
	return e.flipped && !(user && e.seen_by?.includes(user));
}

function ChangedTable({ entries, user }: { entries: ValidityEntry[]; user?: string }) {
	const { t } = useTranslation("w449");
	const { t: tg } = useTranslation("glossary");
	const word = (d: string) => tg(`terms.${d}.label`, { defaultValue: d });
	if (entries.length === 0) {
		return (
			<p className="text-muted-foreground text-sm" data-testid="validity-empty">
				{t("page.empty")}
			</p>
		);
	}
	return (
		<table className="w-full text-sm" data-testid="validity-table">
			<thead>
				<tr className="text-muted-foreground text-left">
					<th className="py-1 pr-3 font-medium">{t("page.comparison")}</th>
					<th className="py-1 pr-3 font-medium">{t("page.rescoredOn")}</th>
					<th className="py-1 pr-3 font-medium">{t("page.was")}</th>
					<th className="py-1 pr-3 font-medium">{t("page.isNow")}</th>
					<th className="py-1 font-medium">{t("page.why")}</th>
				</tr>
			</thead>
			<tbody>
				{entries.map((e) => (
					<tr key={e.id} className={isUnseen(e, user) ? "font-semibold" : undefined} data-testid="validity-row">
						<td className="py-1 pr-3">
							<Link to="/experiments/$expId" params={{ expId: e.exp_id }} className="text-primary hover:underline">
								{e.exp_id}
							</Link>
						</td>
						<td className="py-1 pr-3">{flipDate(e.at)}</td>
						<td className="py-1 pr-3">{word(e.before.decision)}</td>
						<td className="py-1 pr-3">{word(e.after.decision)}</td>
						<td className="py-1">{e.note ?? ""}</td>
					</tr>
				))}
			</tbody>
		</table>
	);
}

function StaleList({ stale }: { stale: ValidityStale[] }) {
	const { t } = useTranslation("w449");
	if (stale.length === 0) return <p className="text-muted-foreground text-sm">{t("page.noneStale")}</p>;
	return (
		<ul className="flex flex-col gap-1 text-sm" data-testid="validity-stale">
			{stale.map((s) => (
				<li key={s.exp_id}>
					<Link to="/experiments/$expId" params={{ expId: s.exp_id }} className="text-primary hover:underline">
						{s.exp_id}
					</Link>{" "}
					<span className="text-muted-foreground">{t("page.updatedChecks", { gates: Object.keys(s.gates).join(", ") })}</span>
				</li>
			))}
		</ul>
	);
}

/** W4-49: "Changed results", the validity ledger of the org. */
export default function ValidityView() {
	const { t } = useTranslation("w449");
	const session = useSession();
	const { data } = useGetValidityQuery();
	const [createJob, job] = useCreateJobMutation();
	const [started, setStarted] = useState(false);
	const user = session.user?.id ?? "local";
	const scan = async () => {
		const res = await createJob({ kind: "validity.scan" });
		if (!("error" in res)) setStarted(true);
	};
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="validity-screen">
			<PageHeader title={t("page.heading")} />
			<Card>
				<CardContent className="pt-4">
					<ChangedTable entries={data?.entries ?? []} user={user} />
				</CardContent>
			</Card>
			<Card>
				<CardContent className="flex flex-col gap-3 pt-4">
					<h2 className="text-sm font-semibold">{t("page.couldChange")}</h2>
					<StaleList stale={data?.stale ?? []} />
					<div className="flex items-center gap-3">
						<Button size="sm" variant="outline" disabled={job.isLoading} onClick={() => void scan()} data-testid="validity-scan">
							{t("page.scan")}
						</Button>
						{started && <span className="text-muted-foreground text-sm">{t("page.scanStarted")}</span>}
					</div>
				</CardContent>
			</Card>
		</div>
	);
}