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
						<TableCell className="bg-destructive/5 font-mono text-xs whitespace-pre-wrap">
							<ConfigValue value={change.a} unsetLabel={t("configDiff.unset")} />
						</TableCell>
						<TableCell className="bg-primary/5 font-mono text-xs whitespace-pre-wrap">
							<ConfigValue value={change.b} unsetLabel={t("configDiff.unset")} />
						</TableCell>
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}

function ConfigValue({ value, unsetLabel }: { value: JsonValue; unsetLabel: string }) {
	if (value === null) return <span className="text-muted-foreground italic">{unsetLabel}</span>;
	return <span>{typeof value === "string" ? value : JSON.stringify(value)}</span>;
}