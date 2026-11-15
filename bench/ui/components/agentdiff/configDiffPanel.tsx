import { DiffText } from "@/components/runs/diffText";
import { EmptyNote } from "@/components/runs/screenStates";
import { ToneBadge } from "@/components/suites/suiteBadges";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { shortHash } from "@/components/verdict/verdict.utils";
import type { AgentDiffConfig, AgentDiffConfigGroup, AgentDiffFieldChange, AgentDiffFileChange, JsonValue } from "@/lib/types/bench";
import { EyeOff } from "lucide-react";
import { useTranslation } from "react-i18next";
import { KIND_TONE, fileDiffAsRunDiff, formatConfigValue, isKnownGroup, sortGroups, testIdPart } from "./agentDiff.utils";

/**
 * The config half: each non-empty group in report order, its changed fields (dotted path, old and
 * new value as inert text) and its laid-down files (hash change, and the text diff when the server
 * had both contents).
 */
export function ConfigDiffPanel({ config }: { config: AgentDiffConfig }) {
	const { t } = useTranslation("bench");
	const groups = sortGroups(config.groups ?? []);
	if (config.identical || groups.length === 0) {
		return <EmptyNote testId="agentdiff-config-identical">{t("agentDiff.config.identical")}</EmptyNote>;
	}
	return (
		<div className="flex min-w-0 flex-col gap-4" data-testid="agentdiff-config">
			{groups.map((group) => (
				<ConfigGroupBlock key={group.id} group={group} />
			))}
		</div>
	);
}

function ConfigGroupBlock({ group }: { group: AgentDiffConfigGroup }) {
	const { t } = useTranslation("bench");
	const fields = group.fields ?? [];
	const files = group.files ?? [];
	const name = isKnownGroup(group.id) ? t(`agentDiff.config.group.${group.id}`) : group.id;
	return (
		<div className="flex min-w-0 flex-col gap-2" data-testid={`agentdiff-group-${group.id}`}>
			<h3 className="text-foreground flex items-center gap-2 text-xs font-semibold tracking-wide uppercase">
				{name}
				<span className="text-muted-foreground font-normal tracking-normal normal-case tabular-nums">
					{t("agentDiff.config.changes", { count: fields.length + files.length })}
				</span>
			</h3>
			{fields.length > 0 && (
				<div className="min-w-0 rounded-sm border">
					{/* Fixed columns, so every group's table lines up with the next. */}
					<Table className="table-fixed" containerClassName="rounded-sm">
						<TableHeader>
							<TableRow className="hover:bg-transparent">
								<TableHead className="w-2/5">{t("agentDiff.config.path")}</TableHead>
								<TableHead className="w-28">{t("agentDiff.config.kind")}</TableHead>
								<TableHead>{t("agentDiff.config.old")}</TableHead>
								<TableHead>{t("agentDiff.config.new")}</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{fields.map((field) => (
								<FieldRow key={field.path} field={field} />
							))}
						</TableBody>
					</Table>
				</div>
			)}
			{files.length > 0 && (
				<ul className="flex min-w-0 flex-col gap-2" aria-label={t("agentDiff.config.files")}>
					{files.map((file) => (
						<FileRow key={file.path} file={file} />
					))}
				</ul>
			)}
		</div>
	);
}

function FieldRow({ field }: { field: AgentDiffFieldChange }) {
	const { t } = useTranslation("bench");
	return (
		<TableRow data-testid={`agentdiff-field-${testIdPart(field.path)}`}>
			<TableCell className="font-mono text-xs break-all whitespace-normal">
				<span className="flex flex-wrap items-center gap-1.5">
					{field.path}
					{field.redacted && (
						<ToneBadge
							tone="neutral"
							icon={EyeOff}
							hint={t("agentDiff.config.redactedHint")}
							testId={`agentdiff-redacted-${testIdPart(field.path)}`}
						>
							{t("agentDiff.config.redacted")}
						</ToneBadge>
					)}
				</span>
			</TableCell>
			<TableCell>
				<ToneBadge tone={KIND_TONE[field.kind] ?? "neutral"}>{t(`agentDiff.kind.${field.kind}`, { defaultValue: field.kind })}</ToneBadge>
			</TableCell>
			<TableCell className="bg-destructive/5 max-w-sm font-mono text-xs break-all whitespace-pre-wrap">
				<ConfigValue value={field.old} />
			</TableCell>
			<TableCell className="bg-primary/5 max-w-sm font-mono text-xs break-all whitespace-pre-wrap">
				<ConfigValue value={field.new} />
			</TableCell>
		</TableRow>
	);
}

function ConfigValue({ value }: { value: JsonValue }) {
	const { t } = useTranslation("bench");
	const text = formatConfigValue(value);
	if (text === null) return <span className="text-muted-foreground font-sans italic">{t("agentDiff.config.unset")}</span>;
	return <span>{text}</span>;
}

function FileRow({ file }: { file: AgentDiffFileChange }) {
	const { t } = useTranslation("bench");
	const id = testIdPart(file.path);
	const hasDiff = Boolean(file.diff && file.diff.length > 0);
	return (
		<li className="bg-card flex min-w-0 flex-col gap-2 rounded-sm border p-3" data-testid={`agentdiff-file-${id}`}>
			<div className="flex min-w-0 flex-wrap items-center gap-2">
				<span className="text-foreground font-mono text-xs break-all">{file.path}</span>
				<ToneBadge tone={KIND_TONE[file.kind] ?? "neutral"}>{t(`agentDiff.kind.${file.kind}`, { defaultValue: file.kind })}</ToneBadge>
				<span className="text-muted-foreground font-mono text-[11px]" data-testid={`agentdiff-file-${id}-hash`}>
					{t("agentDiff.config.hashChange", {
						old: file.old_hash ? shortHash(file.old_hash) : t("agentDiff.config.none"),
						new: file.new_hash ? shortHash(file.new_hash) : t("agentDiff.config.none"),
					})}
				</span>
			</div>
			{hasDiff ? (
				<details className="group min-w-0" data-testid={`agentdiff-file-${id}-details`}>
					<summary className="text-primary cursor-pointer text-xs underline-offset-4 select-none hover:underline">
						{t("agentDiff.config.showDiff")}
					</summary>
					<div className="mt-2 min-w-0">
						<DiffText runDiff={fileDiffAsRunDiff(file)} arm={`agentdiff-${id}`} />
					</div>
				</details>
			) : (
				<p className="text-muted-foreground text-xs" data-testid={`agentdiff-file-${id}-note`}>
					{file.diff_note || t("agentDiff.config.hashOnly")}
				</p>
			)}
			{file.truncated && (
				<p className="text-chart-warning-ink text-xs" data-testid={`agentdiff-file-${id}-truncated`}>
					{t("agentDiff.config.truncated")}
				</p>
			)}
		</li>
	);
}