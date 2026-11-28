import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { InlineError } from "@/components/runs/screenStates";
import type { Incident, IncidentImpact, IncidentStatus } from "@/lib/store/apis/statusApi";
import { type OpenIncidentBody, type UpdateIncidentBody, useOpenIncidentMutation, useUpdateIncidentMutation } from "@/lib/store/apis/statusIncidentsApi";
import "./ufl10I18n";
import { useState } from "react";
import { useTranslation } from "react-i18next";

const IMPACTS: IncidentImpact[] = ["minor", "major", "critical", "maintenance", "none"];
const STATUSES = ["investigating", "identified", "monitoring", "resolved"] as const;

/** The body the open form sends (tested against the Go handler's struct). */
export function incidentBody(title: string, message: string, impact: IncidentImpact): OpenIncidentBody {
	return { title: title.trim(), message: message.trim(), impact };
}

/** The body the note form sends. */
export function noteBody(message: string, status: IncidentStatus): UpdateIncidentBody {
	return { message: message.trim(), status };
}

const selectClass = "border-input bg-background h-9 rounded-md border px-2 text-sm";
const areaClass = "border-input bg-background min-h-20 w-full rounded-md border px-3 py-2 text-sm";

/** Server admins only: post an incident and add notes, the same store `status incident` writes. */
export function IncidentAdmin({ open, onSaved }: { open: Incident[]; onSaved: () => void }) {
	const { t } = useTranslation("ufl10");
	const [title, setTitle] = useState("");
	const [message, setMessage] = useState("");
	const [impact, setImpact] = useState<IncidentImpact>("minor");
	const [openIncident, opening] = useOpenIncidentMutation();
	const [which, setWhich] = useState(open[0]?.id ?? "");
	const [note, setNote] = useState("");
	const [status, setStatus] = useState<IncidentStatus>("monitoring");
	const [updateIncident, updating] = useUpdateIncidentMutation();
	const target = open.some((i) => i.id === which) ? which : (open[0]?.id ?? "");

	const post = async () => {
		const r = await openIncident(incidentBody(title, message, impact));
		if (!("error" in r)) {
			setTitle("");
			setMessage("");
			onSaved();
		}
	};
	const addNote = async () => {
		const r = await updateIncident({ id: target, body: noteBody(note, status) });
		if (!("error" in r)) {
			setNote("");
			onSaved();
		}
	};

	return (
		<section className="flex flex-col gap-6 border-t pt-6" data-testid="status-incident-admin">
			<div className="flex max-w-xl flex-col gap-3">
				<h2 className="text-base font-semibold">{t("incident.heading")}</h2>
				<Field label={t("incident.title")} hint={t("incident.public")}>
					<Input value={title} maxLength={160} onChange={(e) => setTitle(e.target.value)} data-testid="incident-title" />
				</Field>
				<Field label={t("incident.message")}>
					<textarea className={areaClass} value={message} maxLength={2000} onChange={(e) => setMessage(e.target.value)} data-testid="incident-message" />
				</Field>
				<Field label={t("incident.impact")}>
					<select className={selectClass} value={impact} onChange={(e) => setImpact(e.target.value as IncidentImpact)} data-testid="incident-impact">
						{IMPACTS.map((i) => (
							<option key={i} value={i}>
								{t(`incident.impacts.${i}`)}
							</option>
						))}
					</select>
				</Field>
				<InlineError error={opening.error} title={t("incident.error")} testId="incident-open-error" />
				<div>
					<Button size="sm" disabled={!title.trim() || !message.trim() || opening.isLoading} onClick={post} data-testid="incident-post">
						{opening.isLoading ? t("incident.opening") : t("incident.open")}
					</Button>
				</div>
			</div>
			<div className="flex max-w-xl flex-col gap-3">
				<h2 className="text-base font-semibold">{t("incident.noteHeading")}</h2>
				{open.length === 0 ? (
					<p className="text-muted-foreground text-sm" data-testid="incident-none">
						{t("incident.none")}
					</p>
				) : (
					<>
						<Field label={t("incident.which")}>
							<select className={selectClass} value={target} onChange={(e) => setWhich(e.target.value)} data-testid="incident-which">
								{open.map((i) => (
									<option key={i.id} value={i.id}>
										{i.title}
									</option>
								))}
							</select>
						</Field>
						<Field label={t("incident.status")}>
							<select className={selectClass} value={status} onChange={(e) => setStatus(e.target.value as IncidentStatus)} data-testid="incident-status">
								{STATUSES.map((s) => (
									<option key={s} value={s}>
										{t(`incident.statuses.${s}`)}
									</option>
								))}
							</select>
						</Field>
						<Field label={t("incident.message")}>
							<textarea className={areaClass} value={note} maxLength={2000} onChange={(e) => setNote(e.target.value)} data-testid="incident-note" />
						</Field>
						<InlineError error={updating.error} title={t("incident.error")} testId="incident-note-error" />
						<div>
							<Button size="sm" variant="outline" disabled={!note.trim() || updating.isLoading} onClick={addNote} data-testid="incident-add-note">
								{updating.isLoading ? t("incident.adding") : t("incident.addNote")}
							</Button>
						</div>
					</>
				)}
			</div>
		</section>
	);
}
