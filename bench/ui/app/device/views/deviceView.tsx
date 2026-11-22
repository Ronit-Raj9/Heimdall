import { AuthError } from "@/components/platform/authForm";
import { formatRelative } from "@/components/platform/platform.utils";
import { BlockSkeleton } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell/pageHeader";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { getLocale } from "@/lib/i18n";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useApproveDeviceMutation, useDenyDeviceMutation, useGetDeviceQuery } from "@/lib/store/apis/deviceLoginApi";
import { useSession } from "@/lib/session/sessionContext";
import { CheckCircle2, MonitorSmartphone, XCircle } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		cliConnect: typeof import("@/locales/en/cliConnect.json");
	}
}

/** Upper-cases a typed code and puts the dash in the middle: "bcdf ghjk" is BCDF-GHJK. */
export function normalizeUserCode(raw: string): string {
	const c = raw.toUpperCase().replace(/[\s-]/g, "");
	return c.length === 8 ? `${c.slice(0, 4)}-${c.slice(4)}` : c;
}

/**
 * A sign-in code as the server makes them: 8 letters with no vowels, shown as XXXX-XXXX. The alphabet
 * is userCodeAlphabet in auth/device.go; change both together.
 */
const USER_CODE = /^[BCDFGHJKLMNPQRSTVWXZ]{4}-[BCDFGHJKLMNPQRSTVWXZ]{4}$/;

/** Whether the typed or linked text is a sign-in code, after normalizeUserCode. */
export function isUserCode(code: string): boolean {
	return USER_CODE.test(normalizeUserCode(code));
}

/** Whether to say the text is not a code: it is long enough to be one (8 or more letters) and is not. */
export function notACode(raw: string): boolean {
	return raw.replace(/[\s-]/g, "").length >= 8 && !isUserCode(raw);
}

/**
 * The page `heimdall login` sends the user to (/device?code=...): shows which device asks, what
 * access it gets and in which organization, then approves or denies. The token is minted on the
 * server and handed to the terminal; it never shows here.
 */
export default function DeviceView({ initialCode }: { initialCode?: string }) {
	const { t } = useTranslation("cliConnect");
	const session = useSession();
	const [typed, setTyped] = useState(initialCode ?? "");
	const [code, setCode] = useState(initialCode ? normalizeUserCode(initialCode) : "");
	const [done, setDone] = useState<"approved" | "denied" | null>(null);
	// A code that cannot be one is refused here, before any lookup.
	const info = useGetDeviceQuery(code, { skip: !code || !isUserCode(code) || session.authMode === "local" });
	const [approve, approveState] = useApproveDeviceMutation();
	const [deny, denyState] = useDenyDeviceMutation();
	const orgName = session.org?.name ?? session.org?.slug ?? "";

	const header = <PageHeader title={t("device.title")} subtitle={t("device.subtitle")} icon={MonitorSmartphone} />;
	const wrap = (body: ReactNode) => (
		<div className="mx-auto flex w-full max-w-[640px] flex-col gap-6 p-6" data-testid="device-view">
			{header}
			{body}
		</div>
	);

	if (session.authMode === "local") {
		return wrap(
			<div className="bg-card rounded-xl border p-5" data-testid="device-local">
				<p className="font-semibold">{t("device.local")}</p>
				<p className="text-muted-foreground mt-1 text-sm">{t("device.localBody")}</p>
			</div>,
		);
	}
	if (done) {
		const ok = done === "approved";
		const Icon = ok ? CheckCircle2 : XCircle;
		return wrap(
			<div className="bg-card flex items-start gap-3 rounded-xl border p-5" data-testid={`device-${done}`}>
				<Icon className={ok ? "text-chart-success-ink size-5" : "text-muted-foreground size-5"} aria-hidden="true" />
				<div>
					<p className="font-semibold">{t(ok ? "device.approved" : "device.denied")}</p>
					<p className="text-muted-foreground mt-1 text-sm">{ok ? t("device.approvedBody", { org: orgName }) : t("device.deniedBody")}</p>
				</div>
			</div>,
		);
	}

	// The link's own code, still in the field, that cannot be a code (any length).
	const badLink = Boolean(initialCode) && typed === initialCode && !isUserCode(typed);
	const onFind = (event: FormEvent) => {
		event.preventDefault();
		if (isUserCode(typed)) setCode(normalizeUserCode(typed));
	};
	const form = (
		<form className="flex items-end gap-2" onSubmit={onFind} data-testid="device-form">
			<label className="flex flex-1 flex-col gap-1.5 text-sm">
				<span className="font-medium">{t("device.codeLabel")}</span>
				<Input
					value={typed}
					onChange={(event) => setTyped(event.target.value)}
					placeholder="BCDF-GHJK"
					autoComplete="off"
					spellCheck={false}
					className="font-mono tracking-widest uppercase"
					data-testid="device-code"
				/>
				<span className="text-muted-foreground text-xs">{t("device.codeHint")}</span>
				{(notACode(typed) || badLink) && (
					<span className="text-chart-error-ink text-xs" role="alert" data-testid="device-not-a-code">
						{t("device.notACode")}
					</span>
				)}
			</label>
			<Button type="submit" dataTestId="device-find" disabled={!isUserCode(typed)}>
				{t("device.find")}
			</Button>
		</form>
	);

	if (!code) return wrap(form);
	// A linked ?code= that is not a code: the form says why, and nothing was looked up.
	if (!isUserCode(code)) return wrap(form);
	if (info.isLoading) return wrap(<BlockSkeleton rows={3} testId="device-loading" />);
	if (info.isError || !info.data) {
		return wrap(
			<>
				<div className="bg-card rounded-xl border p-5" data-testid="device-not-found">
					<p className="font-semibold">{t("device.notFound")}</p>
					<p className="text-muted-foreground mt-1 text-sm">{t("device.notFoundBody")}</p>
				</div>
				{form}
			</>,
		);
	}

	const d = info.data;
	const expires = formatRelative(d.expires_at, getLocale());
	const err = approveState.error ?? denyState.error;
	const onApprove = async () => {
		try {
			await approve(d.user_code).unwrap();
			setDone("approved");
		} catch {
			// approveState.error renders the failure.
		}
	};
	const onDeny = async () => {
		try {
			await deny(d.user_code).unwrap();
			setDone("denied");
		} catch {
			// denyState.error renders the failure.
		}
	};
	return wrap(
		<div className="bg-card flex flex-col gap-4 rounded-xl border p-5" data-testid="device-pending">
			<p className="font-mono text-2xl font-semibold tracking-widest" data-testid="device-user-code">
				{d.user_code}
			</p>
			<dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
				<dt className="text-muted-foreground">{t("device.client")}</dt>
				<dd>{d.client_name}</dd>
				<dt className="text-muted-foreground">{t("device.address")}</dt>
				<dd className="font-mono">{d.client_ip}</dd>
				<dt className="text-muted-foreground">{t("device.scope")}</dt>
				<dd>{t(`device.scopes.${d.scope}`)}</dd>
				<dt className="text-muted-foreground">{t("device.org")}</dt>
				<dd>{orgName}</dd>
			</dl>
			<p className="text-muted-foreground text-xs">{t("device.warning")}</p>
			{err && <AuthError title={t("device.error")} message={getBenchErrorMessage(err)} testId="device-error" />}
			<div className="flex flex-wrap items-center gap-2">
				<Button onClick={() => void onApprove()} isLoading={approveState.isLoading} dataTestId="device-approve">
					{t("device.approve")}
				</Button>
				<Button variant="outline" onClick={() => void onDeny()} isLoading={denyState.isLoading} dataTestId="device-deny">
					{t("device.deny")}
				</Button>
				{expires && <span className="text-muted-foreground text-xs">{t("device.expires", { when: expires })}</span>}
			</div>
		</div>,
	);
}
