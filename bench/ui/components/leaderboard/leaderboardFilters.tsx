import { UNKNOWN_TRACK, isTrack, type TrackGroup } from "@/components/scenario/scenario.utils";
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { LeaderboardScope, LeaderboardSynthetic } from "@/lib/types/bench";
import type { TFunction } from "i18next";
import { cn, controlledSelectValue } from "@/lib/utils";
import { useId, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

export interface LeaderboardFiltersProps {
	/** The suite the board covers and every suite with sealed runs, newest first. */
	suite: string;
	suites: readonly string[];
	condition: string;
	conditions: readonly string[];
	scope: LeaderboardScope;
	synthetic: LeaderboardSynthetic;
	onSuite: (suite: string) => void;
	onCondition: (condition: string) => void;
	onScope: (scope: LeaderboardScope) => void;
	onSynthetic: (synthetic: LeaderboardSynthetic) => void;
	disabled?: boolean;
	/**
	 * The board's suites grouped by track (from the suites on disk). With onTrack set, a track
	 * filter sits over the suite picker and the picker lists its suites under track headings.
	 */
	trackGroups?: readonly TrackGroup[];
	/** The track filter; null or absent shows every track. */
	track?: string | null;
	onTrack?: (track: string | null) => void;
	/** More switches for the same row (for example the score source picker). */
	children?: ReactNode;
}

/** The track filter's value for "every track". Not snake_case, so never a real track. */
export const ALL_TRACKS = "~all";

/** A track's display name: its label, "Not on disk" for a suite with no folder, or the raw value. */
export function trackLabel(track: string, t: TFunction<"bench">): string {
	if (track === UNKNOWN_TRACK) return t("leaderboard.filters.trackOther");
	return isTrack(track) ? t(`scenario.track.${track}`) : track;
}

/**
 * Track, suite and condition pickers from the response, and the scope and synthetic switches.
 * Values are what the board covers.
 */
export function LeaderboardFilters(props: LeaderboardFiltersProps) {
	const { t } = useTranslation("bench");
	const { suite, suites, condition, conditions, scope, synthetic, disabled, trackGroups, track, onTrack } = props;
	const groups = trackGroups ?? [];
	// Without any suite on disk the tracks are unknown, and the picker stays one flat list.
	const byTrack = onTrack !== undefined && groups.some((g) => g.track !== UNKNOWN_TRACK);
	const activeTrack = byTrack && track && groups.some((g) => g.track === track) ? track : null;
	const shownGroups = activeTrack ? groups.filter((g) => g.track === activeTrack) : groups;
	const listed = byTrack ? shownGroups.flatMap((g) => g.suites) : suites;
	// A value the lists do not carry (an old URL, or a suite outside the track) still shows as selected.
	const suiteOptions = suite && !listed.includes(suite) ? [suite, ...listed] : listed;
	const conditionOptions = condition && !conditions.includes(condition) ? [condition, ...conditions] : conditions;
	const suiteGroups = byTrack
		? [
				...(suite && !listed.includes(suite) ? [{ label: "", options: [suite] }] : []),
				...shownGroups.map((g) => ({ label: trackLabel(g.track, t), options: g.suites })),
			]
		: undefined;

	return (
		<div
			className="flex flex-wrap items-end gap-x-4 gap-y-3"
			role="group"
			aria-label={t("leaderboard.filters.label")}
			data-testid="leaderboard-filters"
		>
			{byTrack && (
				<Picker
					label={t("leaderboard.filters.track")}
					value={activeTrack ?? ALL_TRACKS}
					options={[ALL_TRACKS, ...groups.map((g) => g.track)]}
					labels={Object.fromEntries([
						[ALL_TRACKS, t("leaderboard.filters.allTracks")],
						...groups.map((g) => [g.track, trackLabel(g.track, t)]),
					])}
					onChange={(next) => onTrack(next === ALL_TRACKS ? null : next)}
					disabled={disabled}
					testId="leaderboard-track-picker"
					className="min-w-36 font-sans"
				/>
			)}
			<Picker
				label={t("leaderboard.filters.suite")}
				value={suite}
				options={suiteOptions}
				groups={suiteGroups}
				placeholder={t("leaderboard.filters.noSuites")}
				onChange={props.onSuite}
				disabled={disabled || suiteOptions.length === 0}
				testId="leaderboard-suite-picker"
				className="min-w-52"
			/>
			<Picker
				label={t("leaderboard.filters.condition")}
				value={condition}
				options={conditionOptions}
				onChange={props.onCondition}
				disabled={disabled || conditionOptions.length === 0}
				testId="leaderboard-condition-picker"
				className="min-w-40"
			/>
			<Segmented
				label={t("leaderboard.filters.scope")}
				value={scope}
				options={[
					{ value: "all", label: t("leaderboard.filters.scopeAll"), hint: t("leaderboard.filters.scopeAllHint") },
					{ value: "common", label: t("leaderboard.filters.scopeCommon"), hint: t("leaderboard.filters.scopeCommonHint") },
				]}
				onChange={props.onScope}
				disabled={disabled}
				testId="leaderboard-scope"
			/>
			<Segmented
				label={t("leaderboard.filters.synthetic")}
				value={synthetic}
				options={[
					{ value: "exclude", label: t("leaderboard.filters.syntheticExclude"), hint: t("leaderboard.filters.syntheticExcludeHint") },
					{ value: "only", label: t("leaderboard.filters.syntheticOnly"), hint: t("leaderboard.filters.syntheticOnlyHint") },
				]}
				onChange={props.onSynthetic}
				disabled={disabled}
				testId="leaderboard-synthetic"
			/>
			{props.children}
		</div>
	);
}

interface PickerProps {
	label: string;
	value: string;
	options: readonly string[];
	/** Display text per option value; an option without one shows its value. */
	labels?: Record<string, string>;
	/** Options under headings; replaces the flat list when set. A group with an empty label has no heading. */
	groups?: readonly { label: string; options: readonly string[] }[];
	placeholder?: string;
	onChange: (value: string) => void;
	disabled?: boolean;
	testId: string;
	className?: string;
}

function Picker({ label, value, options, labels, groups, placeholder, onChange, disabled, testId, className }: PickerProps) {
	const id = useId();
	const item = (option: string) => (
		<SelectItem key={option} value={option} className="font-mono text-xs" data-testid={`${testId}-option-${option}`}>
			{labels?.[option] ?? option}
		</SelectItem>
	);
	return (
		<div className="flex flex-col gap-1.5">
			<label htmlFor={id} className="text-muted-foreground text-xs">
				{label}
			</label>
			<Select value={controlledSelectValue(value)} onValueChange={onChange} disabled={disabled}>
				<SelectTrigger id={id} className={cn("font-mono text-xs", className)} data-testid={testId}>
					<SelectValue placeholder={placeholder} />
				</SelectTrigger>
				<SelectContent data-testid={`${testId}-content`}>
					{groups
						? groups.map((group, i) => (
								<SelectGroup key={group.label || `group-${i}`} data-testid={`${testId}-group-${i}`}>
									{group.label && <SelectLabel>{group.label}</SelectLabel>}
									{group.options.map(item)}
								</SelectGroup>
							))
						: options.map(item)}
				</SelectContent>
			</Select>
		</div>
	);
}

interface SegmentedProps<T extends string> {
	label: string;
	value: T;
	options: readonly { value: T; label: string; hint: string }[];
	onChange: (value: T) => void;
	disabled?: boolean;
	testId: string;
}

/** A two-way switch: toggle buttons in a labelled group, the pressed one is the board's value. */
function Segmented<T extends string>({ label, value, options, onChange, disabled, testId }: SegmentedProps<T>) {
	const id = useId();
	return (
		<div className="flex flex-col gap-1.5">
			<span id={id} className="text-muted-foreground text-xs">
				{label}
			</span>
			<div
				role="group"
				aria-labelledby={id}
				className="bg-muted inline-flex h-(--control-h) w-fit items-center gap-0.5 rounded-md border p-0.5"
				data-testid={testId}
			>
				{options.map((option) => {
					const active = option.value === value;
					return (
						<button
							key={option.value}
							type="button"
							aria-pressed={active}
							title={option.hint}
							disabled={disabled}
							onClick={() => {
								if (!active) onChange(option.value);
							}}
							data-testid={`${testId}-${option.value}`}
							className={cn(
								"focus-visible:ring-ring/50 h-full cursor-pointer rounded-[5px] px-3 text-xs font-medium transition-colors duration-100 focus-visible:ring-[3px] focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50",
								active ? "bg-card text-foreground shadow-sm dark:bg-accent" : "text-muted-foreground hover:text-foreground",
							)}
						>
							{option.label}
						</button>
					);
				})}
			</div>
		</div>
	);
}