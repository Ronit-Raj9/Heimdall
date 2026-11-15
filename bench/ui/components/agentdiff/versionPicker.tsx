import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { AgentDiffVersionInfo } from "@/lib/types/bench";
import { ArrowLeftRight } from "lucide-react";
import { useId } from "react";
import { useTranslation } from "react-i18next";
import { findVersion, groupVersionsByAgent, versionLabel } from "./agentDiff.utils";

export interface VersionPickerProps {
	/** Every config hash the run folder knows. */
	versions: readonly AgentDiffVersionInfo[];
	/** The selector each side shows: a config hash, a prefix or <exp_id>:<arm>; "" when none. */
	a: string;
	b: string;
	suite: string;
	condition: string;
	synthetic: boolean;
	/** Suites to suggest in the suite box. */
	suites: readonly string[];
	onA: (selector: string) => void;
	onB: (selector: string) => void;
	onSwap: () => void;
	/** Empty strings clear the param so the server default applies. */
	onScope: (scope: { suite: string; condition: string }) => void;
	onSynthetic: (on: boolean) => void;
	disabled?: boolean;
}

/**
 * Version A and B pickers grouped by agent id, a swap, the suite and condition boxes (applied
 * together) and the synthetic switch. Everything lands in the URL, so a comparison is a link.
 */
export function VersionPicker(props: VersionPickerProps) {
	const { t } = useTranslation("bench");
	const scopeId = useId();
	const syntheticId = useId();
	const { a, b, suite, condition, synthetic, disabled } = props;
	return (
		<div className="flex flex-col gap-3" role="group" aria-label={t("agentDiff.picker.label")} data-testid="agentdiff-picker">
			<div className="flex flex-wrap items-end gap-3">
				<VersionSelect
					label={t("agentDiff.picker.a")}
					value={a}
					versions={props.versions}
					onChange={props.onA}
					disabled={disabled}
					testId="agentdiff-picker-a"
				/>
				<Button
					variant="outline"
					size="icon"
					onClick={props.onSwap}
					disabled={disabled || (!a && !b)}
					aria-label={t("agentDiff.picker.swap")}
					title={t("agentDiff.picker.swap")}
					dataTestId="agentdiff-picker-swap"
				>
					<ArrowLeftRight aria-hidden="true" />
				</Button>
				<VersionSelect
					label={t("agentDiff.picker.b")}
					value={b}
					versions={props.versions}
					onChange={props.onB}
					disabled={disabled}
					testId="agentdiff-picker-b"
				/>
			</div>
			<form
				className="flex flex-wrap items-end gap-3"
				aria-label={t("agentDiff.picker.scope")}
				data-testid="agentdiff-picker-scope"
				onSubmit={(event) => {
					event.preventDefault();
					const data = new FormData(event.currentTarget);
					props.onScope({
						suite: String(data.get("suite") ?? "").trim(),
						condition: String(data.get("condition") ?? "").trim(),
					});
				}}
			>
				<div className="flex flex-col gap-1">
					<label htmlFor={`${scopeId}-suite`} className="text-muted-foreground text-xs">
						{t("agentDiff.picker.suite")}
					</label>
					{/* Keyed by the URL value, so a back navigation resets the box. */}
					<Input
						key={`suite-${suite}`}
						id={`${scopeId}-suite`}
						name="suite"
						defaultValue={suite}
						placeholder={t("agentDiff.picker.suitePlaceholder")}
						list={`${scopeId}-suites`}
						className="w-56 font-mono text-xs"
						disabled={disabled}
						data-testid="agentdiff-picker-suite"
					/>
					<datalist id={`${scopeId}-suites`}>
						{props.suites.map((option) => (
							<option key={option} value={option} />
						))}
					</datalist>
				</div>
				<div className="flex flex-col gap-1">
					<label htmlFor={`${scopeId}-condition`} className="text-muted-foreground text-xs">
						{t("agentDiff.picker.condition")}
					</label>
					<Input
						key={`condition-${condition}`}
						id={`${scopeId}-condition`}
						name="condition"
						defaultValue={condition}
						placeholder={t("agentDiff.picker.conditionPlaceholder")}
						className="w-40 font-mono text-xs"
						disabled={disabled}
						data-testid="agentdiff-picker-condition"
					/>
				</div>
				<Button type="submit" variant="outline" size="sm" disabled={disabled} dataTestId="agentdiff-picker-apply">
					{t("agentDiff.picker.apply")}
				</Button>
				<label
					htmlFor={syntheticId}
					className="text-foreground flex h-8 items-center gap-2 text-xs"
					title={t("agentDiff.picker.syntheticHint")}
				>
					<input
						id={syntheticId}
						type="checkbox"
						checked={synthetic}
						onChange={(event) => props.onSynthetic(event.target.checked)}
						disabled={disabled}
						className="accent-primary size-3.5"
						data-testid="agentdiff-picker-synthetic"
					/>
					{t("agentDiff.picker.synthetic")}
				</label>
			</form>
		</div>
	);
}

interface VersionSelectProps {
	label: string;
	value: string;
	versions: readonly AgentDiffVersionInfo[];
	onChange: (selector: string) => void;
	disabled?: boolean;
	testId: string;
}

function VersionSelect({ label, value, versions, onChange, disabled, testId }: VersionSelectProps) {
	const { t } = useTranslation("bench");
	const id = useId();
	const groups = groupVersionsByAgent(versions);
	// A selector the list does not hold as is (a prefix, an arm, an unknown hash): a prefix of a
	// listed hash selects that hash; anything else still shows as selected, raw.
	const matched = findVersion(versions, value);
	const selected = matched ? matched.config_hash : value;
	const extra = selected && !matched ? selected : null;
	return (
		<div className="flex min-w-0 flex-col gap-1">
			<label htmlFor={id} className="text-muted-foreground text-xs">
				{label}
			</label>
			<Select value={selected || undefined} onValueChange={onChange} disabled={disabled || (versions.length === 0 && !extra)}>
				<SelectTrigger id={id} className="w-auto max-w-full min-w-80 font-mono text-xs" data-testid={testId}>
					<SelectValue placeholder={t("agentDiff.picker.placeholder")} />
				</SelectTrigger>
				<SelectContent data-testid={`${testId}-content`}>
					{extra && (
						<SelectGroup>
							<SelectItem value={extra} className="font-mono text-xs">
								{extra}
							</SelectItem>
						</SelectGroup>
					)}
					{groups.map((group) => (
						<SelectGroup key={group.agent || "~none"}>
							<SelectLabel>{group.agent || t("agentDiff.picker.noAgent")}</SelectLabel>
							{group.versions.map((version) => (
								<SelectItem
									key={version.config_hash}
									value={version.config_hash}
									className="font-mono text-xs"
									title={t("agentDiff.picker.optionHint", { hash: version.config_hash, count: version.sealed_runs })}
								>
									{versionLabel(version)}
								</SelectItem>
							))}
						</SelectGroup>
					))}
				</SelectContent>
			</Select>
		</div>
	);
}