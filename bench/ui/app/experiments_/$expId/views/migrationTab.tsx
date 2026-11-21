import { MigrationReportView } from "@/components/migration/migrationReport";
import { ModeBadge } from "@/components/modeBadge";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { GradeSourceTag } from "@/components/verdict/gradeSourceTag";
import { cn } from "@/lib/utils";
import type { MigrationParams, MigrationResponse } from "@/lib/types/bench";
import { useGetMigrationQuery } from "@/lib/store/apis/migrationApi";
import { parseAsString, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";

/**
 * The Migration tab (docs/model-migration.md): moving from one arm to the others. ?from= is one arm
 * (default A), ?to= a comma list (default every other arm); both are picked from the experiment's arms.
 */
/** The migration request of the tab's URL state; empty values fall back to the server defaults. */
export function migrationParams(
	expId: string,
	search: { from?: string | null; to?: string | null; condition?: string | null; alpha?: string | null; grade_source?: string | null },
): MigrationParams {
	const out: MigrationParams = { expId };
	for (const key of ["from", "to", "condition", "alpha", "grade_source"] as const) {
		const value = search[key];
		if (value) out[key] = value;
	}
	return out;
}

export function MigrationTab({ expId, arms }: { expId: string; arms: readonly string[] }) {
	const { t } = useTranslation("bench");
	const [from, setFrom] = useQueryState("from", parseAsString);
	const [to, setTo] = useQueryState("to", parseAsString);
	const [condition] = useQueryState("condition", parseAsString);
	// alpha and grade_source come along from a bookmark (legacy /migration redirect included); the
	// server honours both, so the report must be the one the address bar names.
	const [alpha] = useQueryState("alpha", parseAsString);
	const [gradeSource] = useQueryState("grade_source", parseAsString);
	const report = useGetMigrationQuery(migrationParams(expId, { from, to, condition, alpha, grade_source: gradeSource }));
	// Only the response to the current selection is shown: RTK keeps the previous one in data while a
	// changed bookmark loads, and its settings would sit beside the new controls (QC2-27-07).
	const current = report.currentData;
	const pending = !current && !report.error;
	const fromArm = from ?? current?.from.arm ?? arms[0];
	const targets = arms.filter((arm) => arm !== fromArm);
	const chosen = to ? to.split(",").filter((arm) => targets.includes(arm)) : targets;
	const toggle = (arm: string) => {
		const next = chosen.includes(arm) ? chosen.filter((a) => a !== arm) : [...chosen, arm];
		// Every other arm is the server default, so the full set leaves the URL clean.
		void setTo(next.length === 0 || next.length === targets.length ? null : targets.filter((a) => next.includes(a)).join(","));
	};

	return (
		<div className="flex flex-col gap-4" data-testid="migration-screen">
			<div className="flex flex-wrap items-end gap-x-6 gap-y-3 text-sm">
				<div className="flex flex-col gap-1">
					<span className="text-muted-foreground text-xs" id="migration-from-label">
						{t("migration.fromLabel")}
					</span>
					<div className="flex flex-wrap gap-1" role="radiogroup" aria-labelledby="migration-from-label">
						{arms.map((arm) => (
							<ArmChip
								key={arm}
								arm={arm}
								active={arm === fromArm}
								role="radio"
								onClick={() => {
									void setFrom(arm === arms[0] ? null : arm);
									void setTo(null);
								}}
								testId={`migration-from-${arm}`}
							/>
						))}
					</div>
				</div>
				<div className="flex flex-col gap-1">
					<span className="text-muted-foreground text-xs" id="migration-to-label">
						{t("migration.toLabel")}
					</span>
					<div className="flex flex-wrap gap-1" role="group" aria-labelledby="migration-to-label">
						{targets.map((arm) => (
							<ArmChip key={arm} arm={arm} active={chosen.includes(arm)} onClick={() => toggle(arm)} testId={`migration-to-${arm}`} />
						))}
					</div>
				</div>
				{current && <EffectiveSettings report={current} />}
			</div>
			{pending ? (
				<BlockSkeleton rows={6} testId="migration-report-loading" />
			) : report.error || !current ? (
				<LoadError title={t("migration.loadError")} error={report.error} onRetry={() => void report.refetch()} testId="migration-error" />
			) : (
				<MigrationReportView report={current} />
			)}
		</div>
	);
}

/** The settings the rendered report was built with, read from the response, never from the URL. */
export function EffectiveSettings({ report }: { report: MigrationResponse }) {
	return (
		<span className="flex items-center gap-2" data-testid="migration-effective">
			<ModeBadge mode={report.mode} />
			<GradeSourceTag source={report.grade_source || undefined} testId="migration-source" />
			<span className="text-muted-foreground text-xs tabular-nums" data-testid="migration-alpha">
				α = {report.alpha}
			</span>
		</span>
	);
}

function ArmChip({
	arm,
	active,
	onClick,
	role,
	testId,
}: {
	arm: string;
	active: boolean;
	onClick: () => void;
	role?: "radio";
	testId: string;
}) {
	return (
		<button
			type="button"
			role={role}
			aria-checked={role ? active : undefined}
			aria-pressed={role ? undefined : active}
			onClick={onClick}
			data-testid={testId}
			className={cn(
				"focus-visible:ring-ring/50 min-w-8 cursor-pointer rounded-sm border px-2.5 py-1 font-mono text-xs transition-colors focus-visible:ring-[3px] focus-visible:outline-none",
				active ? "bg-primary text-primary-foreground border-primary" : "hover:bg-accent",
			)}
		>
			{arm}
		</button>
	);
}