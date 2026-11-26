import "./w441I18n";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { BehaviourRule, RuleKind, StepSelector } from "@/lib/store/apis/behaviourApi";
import { useState } from "react";
import { useTranslation } from "react-i18next";

const KINDS: RuleKind[] = ["present", "absent", "sequence", "before", "repeat", "after_error"];

/** Kinds whose check reads a second step. */
const SECOND_STEP: RuleKind[] = ["sequence", "before", "after_error"];

function cleanSelector(s: StepSelector | undefined): StepSelector | undefined {
	if (!s) return undefined;
	const out: StepSelector = {};
	for (const key of ["class", "tool", "command", "path", "op"] as const) {
		const v = s[key]?.trim();
		if (v) out[key] = v;
	}
	if (s.kinds && s.kinds.length > 0) out.kinds = s.kinds;
	if (s.is_error !== undefined) out.is_error = s.is_error;
	if (s.witnessed) out.witnessed = true;
	return Object.keys(out).length > 0 ? out : undefined;
}

/** The rule as it is sent: empty fields dropped, the second step only for kinds that read one. */
export function cleanRule(r: BehaviourRule): BehaviourRule {
	const out: BehaviourRule = { id: r.id, title: r.title.trim(), kind: r.kind, a: cleanSelector(r.a) ?? {}, version: r.version };
	if (r.description?.trim()) out.description = r.description.trim();
	const b = SECOND_STEP.includes(r.kind) ? cleanSelector(r.b) : undefined;
	if (b) out.b = b;
	if (r.kind === "sequence" && cleanSelector(r.reset)) out.reset = cleanSelector(r.reset);
	if (r.kind === "repeat" && r.n) out.n = r.n;
	if (r.kind === "after_error" && r.window) out.window = r.window;
	return out;
}

/** The next free id of the form my_check_N. */
export function newRuleId(rules: BehaviourRule[]): string {
	const taken = new Set(rules.map((r) => r.id));
	let n = 1;
	while (taken.has(`my_check_${n}`)) n++;
	return `my_check_${n}`;
}

function SelectorFields({ sel, onChange, testId }: { sel: StepSelector; onChange: (next: StepSelector) => void; testId: string }) {
	const { t } = useTranslation("w441");
	const text = (key: "command" | "tool" | "path", label: string) => (
		<label className="flex flex-col gap-1 text-xs">
			{label}
			<Input value={sel[key] ?? ""} onChange={(e) => onChange({ ...sel, [key]: e.target.value })} data-testid={`${testId}-${key}`} />
		</label>
	);
	return (
		<div className="grid gap-2 sm:grid-cols-3">
			{text("command", t("editor.command"))}
			{text("tool", t("editor.tool"))}
			{text("path", t("editor.path"))}
			<label className="flex items-center gap-2 text-xs">
				<input
					type="checkbox"
					checked={Boolean(sel.witnessed)}
					onChange={(e) => onChange({ ...sel, witnessed: e.target.checked })}
					data-testid={`${testId}-witnessed`}
				/>
				{t("editor.witnessed")}
			</label>
		</div>
	);
}

/** The editor of the organization's own behaviour checks (admins, lane I28). */
export function BehaviourEditor({
	initial,
	saving,
	error,
	onSave,
	onCancel,
}: {
	initial: BehaviourRule[];
	saving: boolean;
	error?: string | null;
	onSave: (rules: BehaviourRule[]) => void;
	onCancel: () => void;
}) {
	const { t } = useTranslation("w441");
	const [rules, setRules] = useState<BehaviourRule[]>(initial);
	const update = (i: number, next: Partial<BehaviourRule>) => setRules((rs) => rs.map((r, j) => (j === i ? { ...r, ...next } : r)));
	return (
		<div className="flex flex-col gap-4 text-sm" data-testid="behaviour-editor">
			<h3 className="font-medium">{t("editor.title")}</h3>
			{rules.length === 0 && <p className="text-muted-foreground">{t("editor.empty")}</p>}
			{rules.map((r, i) => (
				<div key={i} className="flex flex-col gap-2 rounded-sm border p-3" data-testid={`behaviour-editor-rule-${i}`}>
					<div className="grid gap-2 sm:grid-cols-2">
						<label className="flex flex-col gap-1 text-xs">
							{t("editor.fieldTitle")}
							<Input
								value={r.title}
								maxLength={80}
								onChange={(e) => update(i, { title: e.target.value })}
								data-testid={`behaviour-editor-title-${i}`}
							/>
						</label>
						<label className="flex flex-col gap-1 text-xs">
							{t("editor.fieldKind")}
							<select
								className="border-input h-9 rounded-md border bg-transparent px-2"
								value={r.kind}
								onChange={(e) => update(i, { kind: e.target.value as RuleKind })}
								data-testid={`behaviour-editor-kind-${i}`}
							>
								{KINDS.map((k) => (
									<option key={k} value={k}>
										{t(`editor.kind.${k}`)}
									</option>
								))}
							</select>
						</label>
					</div>
					<p className="text-muted-foreground text-xs">{t("editor.firstStep")}</p>
					<SelectorFields sel={r.a} onChange={(a) => update(i, { a })} testId={`behaviour-editor-a-${i}`} />
					{SECOND_STEP.includes(r.kind) && (
						<>
							<p className="text-muted-foreground text-xs">{t("editor.laterStep")}</p>
							<SelectorFields sel={r.b ?? {}} onChange={(b) => update(i, { b })} testId={`behaviour-editor-b-${i}`} />
						</>
					)}
					<div>
						<Button size="sm" variant="ghost" onClick={() => setRules((rs) => rs.filter((_, j) => j !== i))}>
							{t("editor.remove")}
						</Button>
					</div>
				</div>
			))}
			{error && (
				<p className="text-destructive text-xs" data-testid="behaviour-editor-error">
					{error}
				</p>
			)}
			<div className="flex flex-wrap gap-2">
				<Button
					size="sm"
					variant="outline"
					onClick={() => setRules((rs) => [...rs, { id: newRuleId(rs), title: "", kind: "present", a: {}, version: 1 }])}
					data-testid="behaviour-editor-add"
				>
					{t("editor.add")}
				</Button>
				<Button size="sm" disabled={saving} onClick={() => onSave(rules.map(cleanRule))} data-testid="behaviour-editor-save">
					{t("editor.save")}
				</Button>
				<Button size="sm" variant="ghost" onClick={onCancel}>
					{t("editor.cancel")}
				</Button>
			</div>
		</div>
	);
}

/** The text of a refused save: the server's sentence, then each check it refused. */
export function saveErrorText(e: unknown, fallback: string): string {
	const data = (e as { data?: { error?: { message?: string }; errors?: { rule: string; error: string }[] } } | null)?.data;
	const parts = [data?.error?.message ?? fallback];
	for (const x of data?.errors ?? []) parts.push(`${x.rule}: ${x.error}`);
	return parts.join(" ");
}