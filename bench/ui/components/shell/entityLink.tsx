import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** The objects an id can point at. */
export type EntityKind = "experiment" | "run" | "suite" | "task" | "agent";

/** Where an id leads: a route, its path params and its search params. */
export interface EntityTarget {
	to: string;
	params?: Record<string, string>;
	search?: Record<string, string>;
}

/**
 * The page of one object. The route owners build these pages; this is the one place that knows
 * their URLs, so every id on every page links the same way.
 *   experiment  /experiments/$expId
 *   run         /runs/$runId
 *   suite       /suites/$suiteId
 *   task        /suites/$suiteId?tab=tasks&task=<id>  (needs the suite)
 *   agent       /agents?agent=<id>
 * A suite given as "<id>@<version>" (as verdicts and runs carry it) links to the suite id; a task
 * keeps the version, so its suite page can say when the current revision differs from the one the
 * evidence ran (QC2-27-09).
 * Returns null when the target cannot be built (a task without its suite, an empty id).
 */
export function entityTarget(kind: EntityKind, id: string, opts: { suite?: string } = {}): EntityTarget | null {
	if (!id) return null;
	switch (kind) {
		case "experiment":
			return { to: "/experiments/$expId", params: { expId: id } };
		case "run":
			return { to: "/runs/$runId", params: { runId: id } };
		case "suite":
			return { to: "/suites/$suiteId", params: { suiteId: suiteIdOf(id) } };
		case "task":
			if (!opts.suite) return null;
			// A versioned ref ("coding-core@1.0.0") opens the suite by its id; the version rides along as rev.
			{
				const rev = suiteVersionOf(opts.suite);
				return {
					to: "/suites/$suiteId",
					params: { suiteId: suiteIdOf(opts.suite) },
					search: rev ? { tab: "tasks", task: id, rev } : { tab: "tasks", task: id },
				};
			}
		case "agent":
			return { to: "/agents", search: { agent: id } };
	}
}

/** The "@<version>" suffix of a suite reference, or null when it has none. */
export function suiteVersionOf(ref: string): string | null {
	const at = ref.indexOf("@");
	return at > 0 && at < ref.length - 1 ? ref.slice(at + 1) : null;
}

/** A suite reference without its "@<version>" suffix. */
export function suiteIdOf(ref: string): string {
	const at = ref.indexOf("@");
	return at > 0 ? ref.slice(0, at) : ref;
}

/** The same target as a plain href, for places that cannot render a router link (copy, export). */
export function entityHref(kind: EntityKind, id: string, opts: { suite?: string } = {}): string | null {
	const target = entityTarget(kind, id, opts);
	if (!target) return null;
	let path = target.to;
	for (const [key, value] of Object.entries(target.params ?? {})) path = path.replace(`$${key}`, encodeURIComponent(value));
	const qs = new URLSearchParams(target.search ?? {}).toString();
	return qs ? `${path}?${qs}` : path;
}

export interface EntityLinkProps {
	kind: EntityKind;
	id: string;
	/** A task's suite; a task link needs it. */
	suite?: string;
	/** Text to show in place of the id (a name). The id stays in the title and the accessible name. */
	children?: ReactNode;
	/** Cut a long id to its first characters (hashes, run ids); the full id stays in the title. */
	short?: number;
	className?: string;
	testId?: string;
	/** -1 keeps the link out of the Tab order (a row that is itself focusable, M7-36). */
	tabIndex?: number;
}

/**
 * An id as a link to its object's page, in the mono font. Every id the UI shows goes through this,
 * so ids are always clickable and always lead to the same place. With nothing to link to (a task
 * without its suite) it renders the id as plain mono text.
 */
export function EntityLink({ kind, id, suite, children, short, className, testId, tabIndex }: EntityLinkProps) {
	const { t } = useTranslation("bench");
	const target = entityTarget(kind, id, { suite });
	const shown = children ?? (short && id.length > short ? `${id.slice(0, short)}…` : id);
	const mono = children === undefined;
	const label = t(`shell.entity.${kind}`, { id });
	if (!target) {
		return (
			<span className={cn(mono && "font-mono text-[0.92em]", "text-foreground", className)} title={id} data-testid={testId}>
				{shown}
			</span>
		);
	}
	return (
		<Link
			// The object routes are typed by the route tree; the target is built for them above.
			to={target.to as never}
			params={target.params as never}
			search={target.search as never}
			title={label}
			tabIndex={tabIndex}
			aria-label={children === undefined && shown !== id ? label : undefined}
			className={cn(
				mono && "font-mono text-[0.92em]",
				"text-foreground decoration-primary/40 hover:text-primary rounded-sm underline decoration-dotted underline-offset-[3px] transition-colors hover:decoration-solid",
				"focus-visible:ring-(--focus) focus-visible:ring-2 focus-visible:outline-none",
				className,
			)}
			data-testid={testId}
			data-entity={kind}
		>
			{shown}
		</Link>
	);
}