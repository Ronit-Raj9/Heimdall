import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { ConfigChange, JsonValue } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";

export interface ConfigDiffProps {
	changes: ConfigChange[];
	/** Column headers; default "A" and "B". */
	armLabels?: { a: string; b: string };
	className?: string;
}

/**
 * The typed config diff between two arms (DESIGN.md 8.6, 18). Values are config data, shown as
 * inert text: strings as they are, everything else as JSON, null as "unset".
 */
export function ConfigDiff({ changes, armLabels, className }: ConfigDiffProps) {
	const { t } = useTranslation("bench");

	if (changes.length === 0) {
		return (
			<p className={cn("text-muted-foreground text-sm", className)} data-testid="config-diff-empty">
				{t("configDiff.empty")}
			</p>
		);
	}

	return (
		<Table className={className} data-testid="config-diff-table">
			<TableHeader>
				<TableRow>
					<TableHead>{t("configDiff.path")}</TableHead>
					<TableHead>{armLabels?.a ?? t("configDiff.armA")}</TableHead>
					<TableHead>{armLabels?.b ?? t("configDiff.armB")}</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{changes.map((change) => (
					<TableRow key={change.path} data-testid={`config-diff-row-${change.path}`}>
						<TableCell className="font-mono text-xs">{change.path}</TableCell>
						<TableCell className="bg-destructive/5 font-mono text-xs break-all whitespace-pre-wrap">
							<ConfigValue value={change.a} unsetLabel={t("configDiff.unset")} />
						</TableCell>
						<TableCell className="bg-primary/5 font-mono text-xs break-all whitespace-pre-wrap">
							<ConfigValue value={change.b} unsetLabel={t("configDiff.unset")} />
						</TableCell>
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}

/** Values longer than this many lines start folded to their first PREVIEW_LINES lines. */
const FOLD_LINES = 6;
const PREVIEW_LINES = 3;

function ConfigValue({ value, unsetLabel }: { value: JsonValue; unsetLabel: string }) {
	const { t } = useTranslation("bench");
	if (value === null) return <span className="text-muted-foreground italic">{unsetLabel}</span>;
	const text = typeof value === "string" ? value : JSON.stringify(value);
	const lines = text.split("\n");
	if (lines.length <= FOLD_LINES) return <span>{text}</span>;
	// A whole config file (for example an agent's config.toml) would fill the page; a native
	// details element keeps the full text one click away without any state.
	return (
		<details data-testid="config-diff-folded">
			<summary className="text-muted-foreground cursor-pointer font-sans">
				<span className="text-foreground font-mono whitespace-pre-wrap">{lines.slice(0, PREVIEW_LINES).join("\n")}</span>
				{"\n"}
				{t("configDiff.showAll", { count: lines.length })}
			</summary>
			<span>{lines.slice(PREVIEW_LINES).join("\n")}</span>
		</details>
	);
}